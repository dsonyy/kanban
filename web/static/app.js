import { init, Terminal, FitAddon } from "./ghostty-web.js";

const $ = (sel, el = document) => el.querySelector(sel);

async function api(method, path, body) {
  const res = await fetch(path, { method, body, credentials: "same-origin" });
  const text = await res.text();
  if (!res.ok) throw new Error(text.replace(/^error: /, "").trim());
  return text;
}

let flashTimer;
function flash(message, { error = false } = {}) {
  const el = $("#flash");
  el.textContent = message;
  el.hidden = false;
  clearTimeout(flashTimer);
  if (!error) flashTimer = setTimeout(() => (el.hidden = true), 3000);
}

async function act(fn) {
  try {
    await fn();
  } catch (err) {
    flash(err.message, { error: true });
  }
}

const view = $("#view");
if (view?.dataset.sse) {
  let busy = false, again = false;
  const refresh = async () => {
    if (busy) return void (again = true);
    busy = true;
    try {
      const open = [...view.querySelectorAll("details[open]")].map((d) => d.dataset.run);
      const doc = new DOMParser().parseFromString(await (await fetch(location.href)).text(), "text/html");
      const next = doc.getElementById("view");
      if (next) view.innerHTML = next.innerHTML;
      for (const run of open) {
        const d = view.querySelector(`details[data-run="${CSS.escape(run)}"]`);
        if (d) d.open = true;
      }
    } finally {
      busy = false;
      if (again) {
        again = false;
        refresh();
      }
    }
  };
  new EventSource("/sse").addEventListener(view.dataset.sse, refresh);
}

document.addEventListener("click", (e) => {
  if (e.target.closest("#flash")) $("#flash").hidden = true;
  const post = e.target.closest("[data-post]");
  if (post) act(() => api("POST", post.dataset.post));
  const open = e.target.closest("[data-open-term]");
  if (open) openTerminal(open.closest("[data-term]"));
});

document.addEventListener("submit", (e) => {
  const form = e.target;
  const data = new FormData(form);
  if (form.matches("[data-new-item]")) {
    e.preventDefault();
    act(async () => {
      await api("POST", `/item/new/${encodeURIComponent(data.get("column"))}`, data.get("content"));
      form.content.value = "";
    });
  } else if (form.matches("[data-move]")) {
    e.preventDefault();
    act(() => api("POST", `/item/${form.dataset.move}/move/${encodeURIComponent(data.get("column"))}`));
  } else if (form.matches("[data-edit-item]")) {
    e.preventDefault();
    act(async () => {
      await api("POST", `/item/${form.dataset.editItem}/edit`, data.get("content"));
      flash("Saved");
    });
  } else if (form.matches("[data-save]")) {
    e.preventDefault();
    act(async () => {
      await api("POST", form.dataset.save, data.get("body"));
      flash("Saved");
    });
  }
});

document.addEventListener("keydown", (e) => {
  if (e.key === "Enter" && (e.ctrlKey || e.metaKey) && e.target.matches("form textarea")) {
    e.preventDefault();
    e.target.form.requestSubmit();
  }
});

document.addEventListener("toggle", (e) => {
  const d = e.target;
  if (d.matches?.("details[data-run]") && d.open && !d.dataset.loaded) {
    d.dataset.loaded = "1";
    act(async () => ($("pre", d).textContent = await api("GET", d.dataset.run)));
  }
}, true);

document.addEventListener("dragstart", (e) => {
  const card = e.target.closest?.(".card");
  if (!card) return;
  e.dataTransfer.setData("text/plain", card.dataset.id);
  e.dataTransfer.effectAllowed = "move";
  card.classList.add("dragging");
});
document.addEventListener("dragend", (e) => {
  e.target.closest?.(".card")?.classList.remove("dragging");
  document.querySelectorAll(".column.drop").forEach((c) => c.classList.remove("drop"));
});
document.addEventListener("dragover", (e) => {
  const col = e.target.closest?.(".column");
  if (!col) return;
  e.preventDefault();
  document.querySelectorAll(".column.drop").forEach((c) => c !== col && c.classList.remove("drop"));
  col.classList.add("drop");
});
document.addEventListener("drop", (e) => {
  const col = e.target.closest?.(".column");
  const id = e.dataTransfer.getData("text/plain");
  if (!col || !id) return;
  e.preventDefault();
  col.classList.remove("drop");
  act(() => api("POST", `/item/${id}/move/${encodeURIComponent(col.dataset.column)}`));
});

let ghostty;
async function openTerminal(el) {
  ghostty ??= init();
  await ghostty;
  el.removeAttribute("data-lazy");
  el.replaceChildren();
  const term = new Terminal({ fontSize: 13, fontFamily: getComputedStyle(document.body).fontFamily, theme: { background: "#000000" } });
  const fit = new FitAddon();
  term.loadAddon(fit);
  term.open(el);
  const ws = new WebSocket(`${location.protocol === "https:" ? "wss" : "ws"}://${location.host}${el.dataset.term}`);
  ws.binaryType = "arraybuffer";
  const early = [];
  const send = (data) => (ws.readyState === WebSocket.OPEN ? ws.send(data) : early.push(data));
  const resize = () => {
    fit.fit();
    if (ws.readyState === WebSocket.OPEN) ws.send(`resize ${term.cols} ${term.rows}`);
  };
  ws.onopen = () => {
    resize();
    early.splice(0).forEach((data) => ws.send(data));
  };
  ws.onmessage = (e) => term.write(new Uint8Array(e.data));
  ws.onclose = () => term.write("\r\n[session closed]\r\n");
  term.onData((d) => send(new TextEncoder().encode(d)));
  // ghostty-web turns the wheel into arrow keys on the alternate screen, which tmux always uses.
  term.attachCustomWheelEventHandler((e) => {
    if (!term.hasMouseTracking() || ws.readyState !== WebSocket.OPEN) return false;
    const r = el.getBoundingClientRect();
    const x = Math.max(1, Math.ceil(((e.clientX - r.left) / r.width) * term.cols));
    const y = Math.max(1, Math.ceil(((e.clientY - r.top) / r.height) * term.rows));
    ws.send(new TextEncoder().encode(`\x1b[<${e.deltaY < 0 ? 64 : 65};${x};${y}M`));
    return true;
  });
  new ResizeObserver(resize).observe(el);
  term.focus();
}

for (const el of document.querySelectorAll("[data-term]:not([data-lazy])")) openTerminal(el);
