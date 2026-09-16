import { init, Terminal, FitAddon } from "ghostty-web";
import "./app.css";
import type { MarkdownEditor } from "./editor";

const $ = (sel: string, el: ParentNode = document): any => el.querySelector(sel);

// The first Escape only leaves a focused field; the next one closes panels and dialogs. Terminals keep Escape for their programs.
document.addEventListener("keydown", (e) => {
  const field = document.activeElement;
  if (e.key !== "Escape" || !field?.matches("input, textarea, select, [contenteditable]") || field.closest(".term")) return;
  e.preventDefault();
  e.stopImmediatePropagation();
  field.blur();
}, true);

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

// Tag input: chips plus a text field. Focus lists every tag in the project, typing filters it,
// Enter takes the highlighted tag or creates the typed one. The chips mirror into a hidden comma separated "tags" field.
const tagInputs = new WeakMap<HTMLElement, (tags: string[]) => void>();

function setTags(form: HTMLElement, tags: string[]) {
  const root = form.querySelector<HTMLElement>("[data-tag-input]");
  if (root) tagInputs.get(root)?.(tags);
}

for (const root of document.querySelectorAll<HTMLElement>("[data-tag-input]")) {
  const text = root.querySelector<HTMLInputElement>('input[type="text"]');
  const hidden = root.querySelector<HTMLInputElement>('input[type="hidden"]');
  const suggest = root.querySelector<HTMLElement>(".tag-suggest");
  const known = new Set((root.dataset.all ?? "").split("\n").filter(Boolean));
  let tags: string[] = [];
  let active = 0;

  const options = () => {
    const q = text.value.trim().toLowerCase();
    const list = [...known].filter((t) => !tags.includes(t) && t.toLowerCase().includes(q));
    if (q && !known.has(text.value.trim()) && !tags.includes(text.value.trim())) list.push(text.value.trim());
    return list;
  };

  const renderSuggest = () => {
    const list = options();
    active = Math.min(active, Math.max(list.length - 1, 0));
    suggest.replaceChildren(...list.map((tag, i) => {
      const row = document.createElement("div");
      row.dataset.tag = tag;
      row.classList.toggle("on", i === active);
      if (!known.has(tag)) {
        const hint = document.createElement("span");
        hint.className = "muted";
        hint.textContent = "Create";
        row.append(hint);
      }
      const name = document.createElement("span");
      name.className = "mono";
      name.textContent = tag;
      row.append(name);
      return row;
    }));
    suggest.hidden = document.activeElement !== text || !list.length;
  };

  const render = () => {
    for (const chip of root.querySelectorAll(".tag")) chip.remove();
    for (const tag of tags) {
      const chip = document.createElement("span");
      chip.className = "tag";
      chip.append(tag);
      const x = document.createElement("button");
      x.type = "button";
      x.textContent = "×";
      x.tabIndex = -1;
      x.setAttribute("aria-label", `Remove ${tag}`);
      x.addEventListener("click", (e) => {
        e.stopPropagation();
        tags = tags.filter((t) => t !== tag);
        render();
      });
      chip.append(x);
      text.before(chip);
    }
    hidden.value = tags.join(",");
    renderSuggest();
  };

  const add = (tag: string) => {
    tag = tag.trim().replace(/,/g, "");
    if (tag && !tags.includes(tag)) tags.push(tag);
    if (tag) known.add(tag);
    text.value = "";
    active = 0;
    render();
  };

  tagInputs.set(root, (list) => {
    tags = [...new Set(list.map((t) => t.trim()).filter(Boolean))];
    text.value = "";
    render();
  });

  root.addEventListener("click", () => text.focus());
  text.addEventListener("focus", renderSuggest);
  text.addEventListener("blur", () => (suggest.hidden = true));
  text.addEventListener("input", () => {
    active = 0;
    renderSuggest();
  });
  suggest.addEventListener("mousedown", (e) => {
    const row = (e.target as HTMLElement).closest<HTMLElement>("[data-tag]");
    e.preventDefault();
    if (row) add(row.dataset.tag);
  });
  text.addEventListener("keydown", (e) => {
    const list = options();
    if (e.key === "Enter" && !e.ctrlKey && !e.metaKey) {
      e.preventDefault();
      if (list.length) add(list[active] ?? text.value);
    } else if (e.key === "," || e.key === "Tab" && text.value.trim()) {
      e.preventDefault();
      add(text.value);
    } else if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      active = (active + (e.key === "ArrowDown" ? 1 : -1) + list.length) % Math.max(list.length, 1);
      renderSuggest();
      suggest.querySelector(".on")?.scrollIntoView({ block: "nearest" });
    } else if (e.key === "Backspace" && !text.value && tags.length) {
      tags.pop();
      render();
    }
  });
  render();
}

// Side panels: [data-open-panel=NAME] opens <aside data-panel=NAME> on the right; data-column preselects a column.
try {
  const width = localStorage.getItem("panel-width");
  if (width) document.documentElement.style.setProperty("--panel-width", width);
} catch {}
document.addEventListener("pointerdown", (e) => {
  const handle = e.target.closest("[data-panel-resize]");
  if (!handle) return;
  e.preventDefault();
  const right = document.documentElement.clientWidth;
  handle.classList.add("dragging");
  handle.setPointerCapture(e.pointerId);
  const move = (ev) => {
    const width = Math.round(Math.min(Math.max(right - ev.clientX, 280), window.innerWidth * 0.7)) + "px";
    document.documentElement.style.setProperty("--panel-width", width);
  };
  const up = () => {
    handle.classList.remove("dragging");
    handle.removeEventListener("pointermove", move);
    try {
      localStorage.setItem("panel-width", getComputedStyle(document.documentElement).getPropertyValue("--panel-width"));
    } catch {}
  };
  handle.addEventListener("pointermove", move);
  handle.addEventListener("pointerup", up, { once: true });
});
document.addEventListener("click", (e) => {
  const open = e.target.closest("[data-open-panel]");
  if (open) {
    const panel = $(`[data-panel="${open.dataset.openPanel}"]`);
    if (!panel) return;
    for (const other of document.querySelectorAll("[data-panel]:not([hidden])")) if (other !== panel) other.hidden = true;
    const column = panel.querySelector("select[name=column]");
    if (column && open.dataset.column) column.value = open.dataset.column;
    panel.hidden = false;
    mountEditors(panel).then(() => {
      const editor = editors.get(panel.querySelector("[data-markdown-editor]"));
      if (editor) editor.focus();
      else panel.querySelector("textarea, input, select")?.focus();
    });
    return;
  }
  const close = e.target.closest("[data-panel-close]");
  if (close) (close.dataset.panelClose ? $(`[data-panel="${close.dataset.panelClose}"]`) : close.closest("[data-panel]")).hidden = true;
});
document.addEventListener("keydown", (e) => {
  if (e.key !== "Escape") return;
  for (const panel of document.querySelectorAll("[data-panel]:not([hidden])")) panel.hidden = true;
});

// Task panel: clicking a card opens it on the right with Task, Terminal and Edit tabs. Ctrl or Cmd click opens the full page.
{
  const panel = $('[data-panel="task"]');
  const project = document.body.dataset.project;
  let current: string | null = null;
  let terminal: { dispose(): void } | null = null;

  const body = (name: string) => panel.querySelector(`[data-tab-body="${name}"]`);
  const data = () => body("task").querySelector("[data-task-data]")?.dataset ?? {};

  const loadTask = async () => {
    if (!current) return;
    const res = await fetch(`/ui/${encodeURIComponent(project)}/item/${current}/panel`, { credentials: "same-origin" });
    if (res.ok) body("task").innerHTML = await res.text();
  };

  const fillEdit = async () => {
    const form = body("edit");
    await mountEditors(form);
    const d = data();
    editors.get(form.querySelector("[data-markdown-editor]"))?.setValue(d.content ?? "");
    setTags(form, (d.tags ?? "").split(","));
    form.column.value = d.column ?? "";
    form.personality.value = d.personality ?? "";
  };

  const showTab = async (name: string) => {
    for (const tab of document.querySelectorAll('[data-panel-tabs="task"] [data-panel-tab]')) tab.classList.toggle("on", tab.dataset.panelTab === name);
    for (const el of panel.querySelectorAll("[data-tab-body]")) el.hidden = el.dataset.tabBody !== name;
    if (name === "terminal" && !terminal) {
      const el = body("terminal").querySelector(".term");
      el.dataset.term = `/term/item/${current}`;
      terminal = await openTerminal(el);
    }
    if (name === "edit") await fillEdit();
  };

  const openTask = async (id: string) => {
    for (const other of document.querySelectorAll("[data-panel]:not([hidden])")) other.hidden = true;
    if (current !== id) {
      terminal?.dispose();
      terminal = null;
      current = id;
      body("task").innerHTML = "";
    }
    panel.hidden = false;
    await loadTask();
    await showTab("task");
  };

  if (panel) {
    document.addEventListener("click", (e) => {
      const card = (e.target as HTMLElement).closest?.<HTMLElement>(".card");
      if (card && !e.ctrlKey && !e.metaKey && !e.shiftKey) {
        e.preventDefault();
        openTask(card.dataset.id);
      }
      const tab = (e.target as HTMLElement).closest?.<HTMLElement>("[data-panel-tab]");
      if (tab?.closest('[data-panel-tabs="task"]')) showTab(tab.dataset.panelTab);
    });

    new EventSource("/sse").addEventListener(`change-${project}`, () => {
      if (!panel.hidden && current && !body("task").hidden) loadTask();
    });

    body("edit").addEventListener("submit", (e) => {
      e.preventDefault();
      const form = e.target as HTMLFormElement;
      act(async () => {
        const d = data();
        const id = current;
        const content = editors.get(form.querySelector("[data-markdown-editor]"))?.getValue() ?? "";
        if (!content.trim()) throw new Error("The task is empty");
        if (content.trim() !== (d.content ?? "").trim()) await api("POST", `/item/${id}/edit`, content);
        const split = (s: string) => [...new Set(s.split(",").map((x) => x.trim()).filter(Boolean))];
        const before = split(d.tags ?? ""), after = split(form.tags.value);
        for (const tag of after.filter((x) => !before.includes(x))) await api("POST", `/item/${id}/tag/${encodeURIComponent(tag)}`);
        for (const tag of before.filter((x) => !after.includes(x))) await api("POST", `/item/${id}/untag/${encodeURIComponent(tag)}`);
        if (form.personality.value !== (d.personality ?? "")) {
          await api("POST", `/item/${id}/personality` + (form.personality.value ? `/${encodeURIComponent(form.personality.value)}` : ""));
        }
        if (form.column.value !== d.column) await api("POST", `/item/${id}/move/${encodeURIComponent(form.column.value)}`);
        await loadTask();
        await showTab("task");
        flash("Saved");
      });
    });
  }
}

// The open panel's tabs live in the views bar, right above the panel and as wide as it.
{
  const slot = $("[data-panel-tab-slot]");
  const sync = () => {
    const open = $("[data-panel]:not([hidden])");
    if (slot) slot.hidden = !open;
    for (const group of document.querySelectorAll("[data-panel-tabs]")) group.hidden = group.dataset.panelTabs !== open?.dataset.panel;
  };
  const watch = new MutationObserver(sync);
  for (const panel of document.querySelectorAll("[data-panel]")) watch.observe(panel, { attributes: true, attributeFilter: ["hidden"] });
  sync();
}

// Column settings: /settings?column=NAME puts the cursor on that column in board.yaml.
{
  const column = new URLSearchParams(location.search).get("column");
  const board = $('form[data-save$="/board/edit"] textarea');
  if (column && board) {
    let offset = 0;
    for (const line of board.value.split("\n")) {
      const m = line.match(/^\s*-\s*name:\s*["']?(.*?)["']?\s*$/);
      if (m && m[1] === column) {
        board.focus();
        board.setSelectionRange(offset, offset + line.length);
        const lineHeight = parseFloat(getComputedStyle(board).lineHeight) || 18;
        board.scrollTop = Math.max(0, board.value.slice(0, offset).split("\n").length - 3) * lineHeight;
        break;
      }
      offset += line.length + 1;
    }
  }
}

// Dev mode: a change to a Go template reloads the page; Vite reloads scripts and styles by itself.
if (document.body.hasAttribute("data-dev")) {
  new EventSource("/sse").addEventListener("reload", () => location.reload());
}

// Markdown editors: <div data-markdown-editor data-placeholder="..."> becomes a live-formatting editor on first use.
const editors = new Map<Element, MarkdownEditor>();
async function mountEditors(root: ParentNode) {
  for (const el of root.querySelectorAll<HTMLElement>("[data-markdown-editor]")) {
    if (editors.has(el)) continue;
    // Loaded on first use so pages without an editor do not download it.
    const { mountMarkdownEditor } = await import("./editor");
    editors.set(el, await mountMarkdownEditor(el, { placeholder: el.dataset.placeholder }));
  }
}

const view = $("#view");
if (view?.dataset.sse) {
  let busy = false, again = false;
  const refresh = async () => {
    if (busy) return void (again = true);
    const typing = document.activeElement;
    if (view.contains(typing) && typing.value) {
      typing.addEventListener("blur", refresh, { once: true });
      return;
    }
    busy = true;
    try {
      const open = [...view.querySelectorAll("details[open]")].map((d) => d.dataset.key);
      const doc = new DOMParser().parseFromString(await (await fetch(location.href)).text(), "text/html");
      const next = doc.getElementById("view");
      if (next) view.innerHTML = next.innerHTML;
      for (const key of open) {
        const d = view.querySelector(`details[data-key="${CSS.escape(key ?? "")}"]`);
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
      const personality = data.get("personality") as string;
      const editor = editors.get(form.querySelector("[data-markdown-editor]"));
      const content = editor ? editor.getValue() : (data.get("content") as string);
      if (!content.trim()) throw new Error("The task is empty");
      const created = await api("POST", `/item/new/${encodeURIComponent(data.get("column") as string)}` + (personality ? `/${encodeURIComponent(personality)}` : ""), content);
      const id = created.match(/^id: (\d+)$/m)[1];
      const tags = [...new Set(String(data.get("tags") ?? "").split(",").map((t) => t.trim()).filter(Boolean))];
      for (const tag of tags) await api("POST", `/item/${id}/tag/${encodeURIComponent(tag)}`);
      setTags(form, []);
      editor?.setValue("");
      form.closest("[data-panel]")?.setAttribute("hidden", "");
    });
  } else if (form.matches("[data-move]")) {
    e.preventDefault();
    act(() => api("POST", `/item/${form.dataset.move}/move/${encodeURIComponent(data.get("column"))}`));
  } else if (form.matches("[data-new-project]")) {
    e.preventDefault();
    createProject(form, false);
  } else if (form.matches("[data-link]")) {
    e.preventDefault();
    act(async () => {
      await api("POST", `/item/${form.dataset.link}/link/${encodeURIComponent(data.get("parent"))}`);
      form.parent.value = "";
    });
  } else if (form.matches("[data-reply]")) {
    e.preventDefault();
    act(async () => {
      await api("POST", `/item/${form.dataset.reply}/reply`, data.get("text"));
      form.text.value = "";
      flash("Sent");
    });
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

// gitInit: undefined asks the server to check for a repository, true runs git init, false creates the project without git.
async function createProject(form, gitInit) {
  const repo = form.repo.value;
  const ask = $("[data-git-init]", form);
  const body = `repo: ${JSON.stringify(repo)}\n` + (gitInit === undefined ? "" : `git_init: ${gitInit}\n`);
  try {
    const board = await api("POST", "/project/new", body);
    location.href = `/ui/${encodeURIComponent(board.match(/^project: (.+)$/m)[1])}`;
  } catch (err) {
    if (gitInit === undefined && err.message.includes("is not a git repository")) {
      $("[data-git-init-path]", ask).textContent = repo;
      ask.hidden = false;
      $("[data-create]", form).hidden = true;
      $("[data-git-init-confirm]", ask).focus();
      return;
    }
    flash(err.message, { error: true });
  }
}

document.addEventListener("click", (e) => {
  const form = e.target.closest("[data-new-project]");
  if (!form) return;
  if (e.target.closest("[data-git-init-confirm]")) createProject(form, true);
  if (e.target.closest("[data-git-init-skip]")) createProject(form, false);
});

// Folder picker: the repository lives on the server's machine, which the browser cannot browse, so the server lists folders.
const picker = $("[data-picker]");
async function openFolder(path) {
  try {
    const res = await fetch(`/ui/dirs?path=${encodeURIComponent(path)}`, { credentials: "same-origin" });
    if (!res.ok) throw new Error((await res.text()).replace(/^error: /, "").trim());
    $("[data-picker-body]", picker).innerHTML = await res.text();
    picker.hidden = false;
    $("[data-picker-body] button", picker)?.focus();
  } catch (err) {
    flash(err.message, { error: true });
  }
}
const closePicker = () => {
  picker.hidden = true;
  $("[data-new-project] [name=repo]")?.focus();
};

document.addEventListener("click", (e) => {
  if (!picker) return;
  if (e.target.closest("[data-pick-path]")) return void openFolder($("[data-new-project] [name=repo]").value || "~");
  const dir = e.target.closest("[data-dir]");
  if (dir) return void openFolder(dir.dataset.dir);
  if (e.target.closest("[data-picker-select]")) {
    const form = $("[data-new-project]");
    form.repo.value = $("[data-picker-current]", picker).dataset.pickerCurrent;
    form.repo.dispatchEvent(new Event("input", { bubbles: true }));
    closePicker();
  }
  if (e.target.closest("[data-picker-cancel]") || e.target === picker) closePicker();
});

document.addEventListener("keydown", (e) => {
  if (picker && !picker.hidden && e.key === "Escape") closePicker();
});

document.addEventListener("input", (e) => {
  const form = e.target.closest("[data-new-project]");
  if (form && e.target.name === "repo") {
    $("[data-git-init]", form).hidden = true;
    $("[data-create]", form).hidden = false;
  }
});

// Keyboard mnemonics: the first free meaningful letter of every button and input label is underlined.
// Pressing it clicks the button or focuses the input; Alt+letter works even while typing.
const mnemonicTargets = "button:not(.tab), a.button, label, .field-label";
const scopeOf = (el) => el.closest("[data-panel]:not([hidden]), [data-picker]:not([hidden]), [data-git-init]:not([hidden]), form, .entry, .actions, main, body");

function assignMnemonics() {
  for (const el of document.querySelectorAll(mnemonicTargets)) {
    if (el.dataset.key || !el.textContent.trim()) continue;
    const scope = scopeOf(el);
    const used = new Set([...scope.querySelectorAll("[data-key]")].filter((o) => scopeOf(o) === scope).map((o) => o.dataset.key));
    const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
    let placed = false;
    for (let node; !placed && (node = walker.nextNode()); ) {
      if (node.parentElement.closest("input, select, textarea")) continue;
      const words = [...node.data.matchAll(/[A-Za-z]/g)];
      const starts = words.filter((m) => m.index === 0 || !/[A-Za-z]/.test(node.data[m.index - 1]));
      for (const m of [...starts, ...words]) {
        const key = m[0].toLowerCase();
        if (used.has(key)) continue;
        // One wrapping span keeps the split text a single box; bare text nodes would become separate flex items.
        const span = document.createElement("span");
        const u = document.createElement("u");
        u.className = "mnemonic";
        u.textContent = m[0];
        span.append(node.data.slice(0, m.index), u, node.data.slice(m.index + 1));
        node.replaceWith(span);
        el.dataset.key = key;
        placed = true;
        break;
      }
    }
    if (!placed) el.dataset.key = "-";
  }
}

new MutationObserver(() => queueMicrotask(assignMnemonics)).observe(document.body, { childList: true, subtree: true });
assignMnemonics();

document.addEventListener("keydown", (e) => {
  if (e.ctrlKey || e.metaKey || e.key.length !== 1 || !/[a-z]/i.test(e.key)) return;
  const typing = e.target.closest?.("input, textarea, select, [contenteditable], .term");
  if (typing && !e.altKey) return;
  const key = e.key.toLowerCase();
  const visible = (el) => el.dataset.key === key && el.getClientRects().length > 0 && !el.disabled;
  const candidates = [...document.querySelectorAll(mnemonicTargets)].filter(visible);
  if (!candidates.length) return;
  const active = document.activeElement && scopeOf(document.activeElement);
  const target = candidates.find((el) => scopeOf(el) === active) ?? candidates.find((el) => el.closest("[data-picker]")) ?? candidates.find((el) => el.closest("[data-git-init]")) ?? candidates[0];
  e.preventDefault();
  if (target.matches("label, .field-label")) (target.closest(".field") ?? target).querySelector("input:not([type=hidden]), select, textarea, [contenteditable]")?.focus();
  else target.click();
});

document.addEventListener("keydown", (e) => {
  const target = e.target as HTMLElement;
  if (e.key === "Enter" && (e.ctrlKey || e.metaKey) && target.closest?.("form textarea, form [data-markdown-editor]")) {
    e.preventDefault();
    target.closest("form").requestSubmit();
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
async function openTerminal(el): Promise<{ dispose(): void }> {
  ghostty ??= init();
  await ghostty;
  el.removeAttribute("data-lazy");
  el.replaceChildren();
  const term = new Terminal({ fontSize: 13, fontFamily: getComputedStyle(document.documentElement).getPropertyValue("--font-mono"), theme: { background: "#000000" } });
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
  const observer = new ResizeObserver(resize);
  observer.observe(el);
  term.focus();
  return {
    dispose() {
      observer.disconnect();
      ws.onclose = null;
      ws.close();
      term.dispose();
      el.replaceChildren();
    },
  };
}

for (const el of document.querySelectorAll("[data-term]:not([data-lazy])")) openTerminal(el);
