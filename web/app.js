"use strict";

const params = new URLSearchParams(location.hash.slice(1));
const token = params.get("token") || "";
const $ = (id) => document.getElementById(id);

const state = {
  inputs: [], // { name, text }, the first one is the reference
  result: null,
  filter: "all",
  reveal: params.get("reveal") === "1",
  keysOnly: params.get("keys") === "1",
};

const FILTERS = [
  ["all", "All"],
  ["missing", "Missing"],
  ["extra", "Extra"],
  ["differs", "Differs"],
  ["near", "Near-misses"],
];
const MARKS = { missing: "^", extra: "+", differs: "≠", near: "≈", same: "" };
const letter = (i) => String.fromCharCode(65 + (i % 26)) + (i >= 26 ? Math.floor(i / 26) : "");

async function api(path, body) {
  const res = await fetch(path, {
    method: body ? "POST" : "GET",
    headers: { Authorization: "Bearer " + token, "Content-Type": "application/json" },
    body: body && JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({ error: res.statusText }));
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

async function refresh() {
  showError("");
  if (state.inputs.length === 0) {
    state.result = null;
  } else {
    try {
      state.result = await api("/api/diff", { inputs: state.inputs, keysOnly: state.keysOnly });
    } catch (e) {
      showError(e.message); // keep the last good proof on screen
    }
  }
  render();
}

function showError(msg) {
  $("error").hidden = !msg;
  $("error").textContent = msg ? "✗ " + msg : "";
}

const el = (tag, cls, text) => {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text != null) n.textContent = text;
  return n;
};

function render() {
  const r = state.result;
  const sources = r ? r.sources : [];
  $("proof-date").textContent = new Date().toLocaleDateString("en-GB", { day: "numeric", month: "short", year: "numeric" });
  $("proof-sources").textContent = sources.length;
  $("proof-keys").textContent = r ? r.rows.length : 0;
  $("reveal").checked = state.reveal;
  $("keys-only").checked = state.keysOnly;
  document.body.classList.toggle("revealed", state.reveal);
  renderSlips(r);
  renderFilters(r);
  renderTable(r);
}

function renderSlips(r) {
  const list = $("slips");
  list.replaceChildren();
  state.inputs.forEach((input, i) => {
    const li = el("li", "slip" + (i === 0 ? " ref" : ""));
    const top = el("div", "slip-top");
    top.append(el("span", "slip-name", input.name));
    if (i === 0) top.append(el("span", "stamp", "reference"));
    li.append(top);
    const meta = el("ul", "slip-meta");
    (r ? r.sources : []).forEach((s, j) => {
      if (r.from[j] !== i) return;
      const m = el("li");
      m.append(el("b", "col", letter(j)), el("span", "fmt", s.format), document.createTextNode(`${s.keys ? s.keys.length : 0} keys`));
      if (s.name.includes("#")) m.append(el("span", "sub", "#" + s.name.split("#").pop()));
      meta.append(m);
    });
    li.append(meta);
    const actions = el("div", "slip-actions");
    if (i > 0) actions.append(button("make reference", () => { state.inputs.unshift(...state.inputs.splice(i, 1)); refresh(); }));
    actions.append(button("remove", () => { state.inputs.splice(i, 1); refresh(); }));
    li.append(actions);
    list.append(li);
  });
  const add = el("li", "slip add");
  const b = button("", () => openPaste());
  b.append(el("span", "plus", "+"), el("span", null, state.inputs.length < 2 ? "add a source" : "add another"), el("small", null, "paste, choose or drop a file"));
  add.append(b);
  list.append(add);
}

function button(text, onClick) {
  const b = el("button", null, text);
  b.type = "button";
  b.addEventListener("click", onClick);
  return b;
}

function renderFilters(r) {
  const box = $("filters");
  box.replaceChildren();
  for (const [key, label] of FILTERS) {
    const n = !r ? 0 : key === "all" ? r.rows.length : r.counts[key] || 0;
    const b = button("", () => { state.filter = key; render(); });
    b.setAttribute("role", "tab");
    b.setAttribute("aria-selected", state.filter === key);
    b.className = `f-${key}` + (n === 0 ? " zero" : "");
    if (MARKS[key]) b.append(el("b", "fm", MARKS[key]));
    b.append(el("span", null, label), el("span", "count", String(n)));
    box.append(b);
  }
}

function renderTable(r) {
  const empty = !r || r.sources.length < 2;
  $("empty").hidden = !empty;
  $("proof").hidden = empty;
  if (empty) {
    const p = $("empty").querySelector("strong");
    p.textContent = state.inputs.length === 1 ? "Add one more source to compare against the reference." : "Drop two or more files anywhere on this sheet.";
    return;
  }
  const head = $("head");
  head.replaceChildren(el("th", "n", "ln"), el("th", "mark"), el("th", "key", "Key"));
  r.sources.forEach((s, i) => {
    const th = el("th", "src" + (i === 0 ? " ref" : ""));
    th.append(el("b", "col", letter(i)), el("span", "src-name", s.name));
    th.title = `${s.name} · ${s.format}`;
    head.append(th);
  });
  head.append(el("th", "note", "Margin"));

  const body = $("rows");
  body.replaceChildren();
  r.rows.forEach((row, idx) => {
    if (state.filter !== "all" && row.status !== state.filter) return;
    const tr = el("tr", row.status);
    tr.append(el("td", "n", String(idx + 1).padStart(2, "0")), el("td", "mark", MARKS[row.status]), el("td", "key", row.key));
    row.cells.forEach((c, i) => tr.append(cell(row, c, i)));
    tr.append(margin(row, r));
    body.append(tr);
  });
  if (!body.children.length) {
    const tr = el("tr", "none");
    const td = el("td", null, state.filter === "all" ? "" : "Nothing to mark here. Clean proof.");
    td.colSpan = r.sources.length + 4;
    tr.append(td);
    body.append(tr);
  }
}

function cell(row, c, i) {
  const td = el("td", "val" + (c.note ? " " + (c.note === "differs" ? "differs" : "near") : ""));
  if (!c.present) {
    td.classList.add("absent");
    if (row.status === "missing") td.append(el("span", "caret", "^ missing"));
    else td.append(el("span", "dash", "—"));
    return td;
  }
  if (row.status === "extra") td.classList.add("extra");
  const ref = row.cells[0];
  td.append(renderValue(c.value, c.note, ref.present ? ref.value : null));
  if (c.note === "double base64") td.firstChild.append(el("span", "tag", "b64²"));
  if (state.reveal) td.title = c.value;
  return td;
}

// renderValue prints a value with its tells, the characters that make a near-miss, in amber.
// Masked values keep their tells too, so a trailing newline shows without the secret.
function renderValue(v, note, ref) {
  const out = el("span", "v");
  if (v === "") {
    out.append(el("span", "emptyv", "empty"));
    return out;
  }
  const lead = v.match(/^\s*/)[0];
  const trail = lead.length === v.length ? "" : v.match(/\s*$/)[0];
  let core = v.slice(lead.length, v.length - trail.length);
  let qOpen = "", qClose = "";
  if (note === "quotes" && /^["'].*["']$/s.test(core)) {
    qOpen = core[0]; qClose = core[core.length - 1]; core = core.slice(1, -1);
  }
  // The middle shortens with an ellipsis; the tells either side never do.
  const mid = el("span", "core");
  if (!state.reveal) {
    mid.append(el("span", "mask", "•".repeat(Math.min(Math.max(core.length, 4), 12))));
  } else if (note === "case" && ref != null) {
    const r = ref.trim();
    [...core].forEach((ch, k) => mid.append(ch !== r[k] ? el("span", "tell", ch) : document.createTextNode(ch.replace(/\n/g, "↵"))));
  } else {
    mid.append(...inner(core));
  }
  out.append(...tells(lead), ...(qOpen ? [el("span", "tell", qOpen)] : []), mid, ...(qClose ? [el("span", "tell", qClose)] : []), ...tells(trail));
  return out;
}

const GLYPH = { " ": "·", "\n": "↵", "\r": "␍", "\t": "→" };
const tells = (s) => [...s].map((ch) => el("span", "tell", GLYPH[ch] || ch));
const inner = (s) => s.split(/(\n)/).map((part) => (part === "\n" ? el("span", "nl", "↵") : document.createTextNode(part)));

function margin(row, r) {
  const td = el("td", "note");
  const cols = (pred) => row.cells.map((c, i) => (pred(c, i) ? letter(i) : null)).filter(Boolean).join(", ");
  if (row.status === "missing") td.textContent = `insert in ${cols((c) => !c.present)}`;
  else if (row.status === "extra") td.textContent = `not in reference · ${cols((c) => c.present)}`;
  else if (row.status === "same") td.textContent = "";
  else td.textContent = row.cells.map((c, i) => (c.note ? `${c.note} · ${letter(i)}` : null)).filter(Boolean).join("; ");
  return td;
}

// Paste and drop.
function openPaste() {
  $("paste").hidden = false;
  $("paste-name").value = "";
  $("paste-text").value = "";
  $("paste-text").focus();
}

$("paste").addEventListener("submit", (e) => {
  e.preventDefault();
  const text = $("paste-text").value;
  if (!text.trim()) return;
  state.inputs.push({ name: $("paste-name").value.trim() || `pasted-${state.inputs.length + 1}`, text });
  $("paste").hidden = true;
  refresh();
});
$("paste-cancel").addEventListener("click", () => { $("paste").hidden = true; });
$("file").addEventListener("change", async (e) => {
  await addFiles(e.target.files);
  e.target.value = "";
  $("paste").hidden = true;
});

async function addFiles(files) {
  for (const f of files) state.inputs.push({ name: f.name, text: await f.text() });
  refresh();
}

let dragDepth = 0;
window.addEventListener("dragenter", (e) => { e.preventDefault(); dragDepth++; $("drop").hidden = false; });
window.addEventListener("dragleave", () => { if (--dragDepth <= 0) { dragDepth = 0; $("drop").hidden = true; } });
window.addEventListener("dragover", (e) => e.preventDefault());
window.addEventListener("drop", (e) => {
  e.preventDefault();
  dragDepth = 0;
  $("drop").hidden = true;
  if (e.dataTransfer.files.length) addFiles(e.dataTransfer.files);
});

$("reveal").addEventListener("change", (e) => { state.reveal = e.target.checked; render(); });
$("keys-only").addEventListener("change", (e) => { state.keysOnly = e.target.checked; refresh(); });

api("/api/inputs")
  .then((d) => { state.inputs = d.inputs || []; return refresh(); })
  .catch((e) => { showError(e.message); render(); });
