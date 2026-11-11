package main

import "testing"

func TestNearMiss(t *testing.T) {
	for _, c := range [][3]string{
		{"abc", "abc\n", "trailing newline"},
		{"abc", " abc", "whitespace"},
		{"info", `"info"`, "quotes"},
		{"staging", "Staging", "case"},
		{"sk_test_1", "c2tfdGVzdF8x", "double base64"},
		{"sk_test_1", "c2tfdGVzdF8xCg==", "double base64"}, // echo | base64 adds a newline
		{"a", "b", ""},
		{"abcd", "YWJjZA", ""}, // unpadded, so not what a Secret would hold
	} {
		if got := nearMiss(c[0], c[1]); got != c[2] {
			t.Errorf("nearMiss(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

func TestDemo(t *testing.T) {
	srcs, err := loadAll([]string{"testdata/demo/.env.staging", "testdata/demo/staging-secrets.yaml", "testdata/demo/compose.yml#api"})
	if err != nil {
		t.Fatal(err)
	}
	res := compare(srcs, false)
	got := map[string]string{}
	for _, r := range res.Rows {
		got[r.Key] = r.Status + " " + r.Detail
	}
	for k, want := range map[string]string{
		"PORT":                        "same ",
		"APP_ENV":                     "near case in testdata/demo/compose.yml#api",
		"LOG_LEVEL":                   "near quotes in testdata/demo/compose.yml#api",
		"REDIS_URL":                   "near whitespace in testdata/demo/compose.yml#api",
		"STRIPE_SECRET_KEY":           "near double base64 in testdata/demo/staging-secrets.yaml#api-env",
		"JWT_SECRET":                  "near trailing newline in testdata/demo/staging-secrets.yaml#api-env",
		"DATABASE_URL":                "differs differs in testdata/demo/compose.yml#api",
		"SENTRY_DSN":                  "missing missing in testdata/demo/compose.yml#api",
		"S3_BUCKET":                   "missing missing in testdata/demo/staging-secrets.yaml#api-env",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "extra only in testdata/demo/staging-secrets.yaml#api-env",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
	if !res.Changed() {
		t.Fatal("the demo has differences")
	}
	if keys := compare(srcs, true); keys.Counts[Near]+keys.Counts[Differs] != 0 {
		t.Fatalf("keys only still compared values: %v", keys.Counts)
	}
}
