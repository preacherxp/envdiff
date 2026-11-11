package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

//go:embed web
var webFS embed.FS

const usage = `envdiff compares environment variables across .env files, Kubernetes
ConfigMaps and Secrets, and Docker Compose services.

  envdiff [flags] REFERENCE OTHER...     print the differences; exit 1 if there are any
  envdiff serve [flags] [FILE...]        open the side-by-side web UI

Inputs are paths, or - for stdin. Add #name to pick one ConfigMap, Secret or
Compose service from a file: compose.yml#api. Without it every one is compared.

Flags:
`

func main() {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		os.Exit(serveCmd(os.Args[2:]))
	}
	os.Exit(diffCmd(os.Args[1:]))
}

// split lets flags come after the files: envdiff a b --json.
func split(args []string) (flags, files []string) {
	for _, a := range args {
		if len(a) > 1 && a[0] == '-' {
			flags = append(flags, a)
		} else {
			files = append(files, a)
		}
	}
	return
}

func diffCmd(args []string) int {
	set := flag.NewFlagSet("envdiff", flag.ContinueOnError)
	set.Usage = func() { fmt.Fprint(os.Stderr, usage); set.PrintDefaults() }
	asJSON := set.Bool("json", false, "print the comparison as JSON")
	all := set.Bool("all", false, "list keys that match too")
	show := set.Bool("show", false, "print values (they are hidden by default, as they are often secrets)")
	keysOnly := set.Bool("keys", false, "compare key names only, for placeholder files like .env.example")
	flags, files := split(args)
	if err := set.Parse(flags); err != nil {
		return 2
	}
	srcs, err := loadAll(files)
	if err != nil {
		fmt.Fprintln(os.Stderr, "envdiff:", err)
		return 2
	}
	res := compare(srcs, *keysOnly)
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(res)
	} else {
		fi, _ := os.Stdout.Stat()
		color := os.Getenv("NO_COLOR") == "" && fi != nil && fi.Mode()&os.ModeCharDevice != 0
		printText(os.Stdout, res, color, *all, *show)
	}
	if res.Changed() {
		return 1
	}
	return 0
}

func loadAll(files []string) ([]*Source, error) {
	var srcs []*Source
	for _, f := range files {
		s, err := load(f)
		if err != nil {
			return nil, err
		}
		srcs = append(srcs, s...)
	}
	if len(srcs) < 2 {
		return nil, errors.New("need at least two sources to compare (run envdiff -h)")
	}
	return srcs, nil
}

var marks = map[string]string{Missing: "^", Extra: "+", Differs: "≠", Near: "≈", Same: " "}

func printText(w io.Writer, r Result, color, all, show bool) {
	paint := func(code, s string) string {
		if !color {
			return s
		}
		return "\x1b[" + code + "m" + s + "\x1b[0m"
	}
	tone := map[string]string{Missing: "36", Extra: "36", Differs: "33", Near: "1;33", Same: "2"}
	width := 0
	for _, row := range r.Rows {
		width = max(width, len(row.Key))
	}
	for i, s := range r.Sources {
		role := ""
		if i == 0 {
			role = paint("2", "  reference")
		}
		fmt.Fprintf(w, "%s %s  %s%s\n", paint("2", fmt.Sprintf("%-9s", s.Format)), s.Name, paint("2", strconv.Itoa(len(s.Keys))+" keys"), role)
	}
	fmt.Fprintln(w)
	for _, row := range r.Rows {
		if row.Status == Same && !all {
			continue
		}
		fmt.Fprintf(w, "%s %s  %s\n", paint(tone[row.Status], marks[row.Status]), paint(tone[row.Status], fmt.Sprintf("%-*s", width, row.Key)), row.Detail)
		if show {
			for i, c := range row.Cells {
				if c.Present {
					fmt.Fprintf(w, "    %s %s\n", paint("2", r.Sources[i].Name), strconv.Quote(c.Value))
				}
			}
		}
	}
	if !r.Changed() {
		fmt.Fprintln(w, paint("36", "✓ no differences"))
	}
	fmt.Fprintln(w, "\n"+paint("2", summary(r)))
}

// input is one file as the browser holds it: a name, optionally with #pick, and its text.
type input struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

func serveCmd(args []string) int {
	set := flag.NewFlagSet("envdiff serve", flag.ContinueOnError)
	set.Usage = func() { fmt.Fprint(os.Stderr, usage); set.PrintDefaults() }
	addr := set.String("addr", "127.0.0.1:0", "listen address (keep it on loopback)")
	noOpen := set.Bool("no-open", false, "do not open a browser")
	flags, files := split(args)
	if err := set.Parse(flags); err != nil {
		return 2
	}
	var inputs []input
	for _, f := range files {
		path, pick, _ := strings.Cut(f, "#")
		b, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "envdiff:", err)
			return 2
		}
		name := filepath.Base(path)
		if pick != "" {
			name += "#" + pick
		}
		inputs = append(inputs, input{name, string(b)})
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := serve(ctx, *addr, *noOpen, inputs); err != nil {
		fmt.Fprintln(os.Stderr, "envdiff:", err)
		return 1
	}
	return 0
}

func serve(ctx context.Context, addr string, noOpen bool, inputs []input) error {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	token := hex.EncodeToString(b)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer ln.Close()
	origin := "http://" + ln.Addr().String()
	url := origin + "/#token=" + token
	fmt.Println(url)
	if !noOpen {
		_ = openBrowser(url)
	}
	server := &http.Server{Handler: routes(token, origin, inputs), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	if err := server.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func routes(token, origin string, inputs []input) http.Handler {
	static, _ := fs.Sub(webFS, "web")
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(static))
	api := func(pattern string, h func(*http.Request) (any, error)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+token {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if o := r.Header.Get("Origin"); o != "" && o != origin {
				http.Error(w, "bad origin", http.StatusForbidden)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			v, err := h(r)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				v = map[string]string{"error": err.Error()}
			}
			_ = json.NewEncoder(w).Encode(v)
		})
	}

	api("GET /api/inputs", func(*http.Request) (any, error) {
		return map[string]any{"inputs": inputs}, nil
	})

	api("POST /api/diff", func(r *http.Request) (any, error) {
		var req struct {
			Inputs   []input
			KeysOnly bool
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return nil, err
		}
		var srcs []*Source
		var from []int // which input each source came from
		for i, in := range req.Inputs {
			name, pick, _ := strings.Cut(in.Name, "#")
			s, err := parseNamed(name, pick, in.Text)
			if err != nil {
				return nil, err
			}
			srcs = append(srcs, s...)
			for range s {
				from = append(from, i)
			}
		}
		return struct {
			Result
			From []int `json:"from"`
		}{compare(srcs, req.KeysOnly), from}, nil
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	}
	return exec.Command("xdg-open", url).Start()
}
