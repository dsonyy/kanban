# Faza 0 - wynik

Data: 2026-09-16. Kod spike'ów w `spike/`, do wyrzucenia po Fazie 1.
Środowisko: Go 1.27.1, tmux 3.6, Claude Code 2.1.273, Codex CLI 0.154.0, ghostty-web 0.4.0.

## Decyzje

| Spike | Decyzja |
|---|---|
| S1 terminal | ghostty-web bez kroku builda. Fallback na xterm.js niepotrzebny. |
| S2 hooki | Jeden wspólny plik hooków wstrzykiwany flagą, task z env `KANBAN_TASK`, payload hooka przekazywany surowo jako body. |
| S3 watcher | fsnotify, własna rekursja po katalogach, filtr plików tymczasowych, debounce per plik, ignorowanie własnych zapisów. |
| S4 rate limit | Odłożone decyzją użytkownika. Wraca w Fazie 5. |

## S1 - terminal w przeglądarce

Stos: Go `creack/pty` uruchamia `tmux attach -t <sesja>`, `coder/websocket` przenosi bajty. Ramki binarne to dane terminala, ramka tekstowa `resize <cols> <rows>` to zmiana rozmiaru.

ghostty-web ładuje się jako zwykły moduł ES (`ghostty-web.js` + `ghostty-vt.wasm` ze ścieżki względnej), serwowany przez `http.FileServer`. Zero builda, punkt 18 stoi.

Sprawdzone i działa:
- TUI Claude Code: kolory, ramki tabel, pasek statusu tmuxa
- wpisywanie, Enter, strzałki (historia promptów), ctrl-c
- resize na żywo: 215x68 → 76x25 w przeglądarce, tmux dostaje dokładnie to samo, TUI się przerysowuje
- reconnect: przeładowanie strony wraca z pełnym stanem ekranu, bo tmux rysuje go od nowa
- brak wycieków: po kilku przeładowaniach dokładnie jeden klient tmuxa na sesję
- masowy output (300k linii): tmux przyjmuje go w 0,18s, a do przeglądarki idą tylko zmiany ekranu. tmux działa jako naturalny bufor chroniący przeglądarkę.

Znalezione problemy:
- **Kółko myszy nie przewija.** ghostty-web w alternate screen, w którym zawsze jest tmux, zamienia kółko na strzałki. Poprawka: `attachCustomWheelEventHandler`, który przy włączonym mouse trackingu wysyła sekwencję SGR (`ESC[<64;x;yM` / `65`). Po poprawce tmux wchodzi w copy mode i przewija. 8 linii w kliencie.
- **Sesje zarządzane przez aplikację wymagają własnych opcji tmuxa:** `mouse on` (inaczej poprawka wyżej nie ma czego wysłać) i `focus-events on` (Claude Code sam o to prosi w pasku).

Nie sprawdzone: telefon i dotyk, schowek (kopiowanie z copy mode do przeglądarki), opóźnienie przez Tailscale. Wracają przy Fazie 3 i 4.

## S2 - hooki

### Claude Code

- Hooki wstrzykiwane przez `claude --settings <plik>`. Nic nie trafia do repo, punkt 11 stoi.
- Payload JSON na stdin, w każdym zdarzeniu: `session_id`, `transcript_path`, `cwd`, `hook_event_name`.
- `Stop` niesie `last_assistant_message`. `Notification` niesie `notification_type`, np. `idle_prompt` po 60s bez inputu. Stan agenta da się rozpoznać bez parsowania tekstu.
- Env procesu nadrzędnego dociera do hooka. `KANBAN_TASK=123` ustawione przy starcie agenta jest widoczne w każdym hooku.
- Zaobserwowane zdarzenia: SessionStart, UserPromptSubmit, PreToolUse, Stop, Notification, SessionEnd.

### Codex

- Ten sam format hooków co Claude Code.
- Wstrzykiwane przez `-c 'hooks.Stop=[{hooks=[{type="command",command="..."}]}]'`, bez plików w repo.
- Hooki spoza zaufanej konfiguracji wymagają `--dangerously-bypass-hook-trust` albo wpisu w `hooks.state` w `~/.codex/config.toml`.
- Payload: `session_id`, `turn_id`, `transcript_path` (`~/.codex/sessions/RRRR/MM/DD/rollout-*.jsonl`), `model`, `last_assistant_message`.
- Env przechodzi tak samo.

### Decyzja o kształcie

`kanban hook <zdarzenie>` ≡ `POST /hook/<zdarzenie>`. Body to surowy JSON ze stdin, task pochodzi z `KANBAN_TASK`.

Dlaczego env, a nie ID w argumentach: przy ID w argumentach każdy task potrzebowałby własnego pliku hooków. Z env wystarcza jeden plik na harness, a to aplikacja i tak ustawia env przy starcie agenta (punkt 28). Jawne `kanban hook stop 123` zostaje dozwolone i ma pierwszeństwo przed env.

Koszt: ~16ms na wywołanie skryptu shellowego. Binarka Go łącząca się po sockecie będzie w tym samym rzędzie albo szybsza. Nie jest to wąskie gardło.

### Znaleziony problem: dialog zaufania

Claude Code przy pierwszym uruchomieniu w nowym katalogu pyta, czy mu ufać, i blokuje start do odpowiedzi. Worktree per task to zawsze nowy katalog, więc każdy task by na tym stawał. Trzeba to obsłużyć w Fazie 2: albo wpisać worktree jako zaufany przed startem agenta, albo wykrywać dialog i emitować ACTION REQUIRED. Pierwsze jest lepsze, do sprawdzenia, gdzie Claude Code trzyma listę zaufanych katalogów.

Nie sprawdzone: `Notification` od promptu o uprawnienia (twoje globalne ustawienia zezwalają na komendy, więc prompt się nie pojawił), `claude --resume <session_id>` wewnątrz tmuxa.

## S3 - watcher plików

fsnotify na Linuksie, obserwowany katalog z plikami YAML i MD pod gitem.

| Wzorzec zapisu | Zdarzenia |
|---|---|
| atomic write (tmp + rename) | CREATE/WRITE/RENAME na pliku tymczasowym, CREATE na docelowym |
| vim | CREATE/REMOVE na `.swp` i `.swx`, WRITE na pliku |
| zwykły zapis (jak Obsidian) | WRITE |
| `git stash` i `stash pop` | REMOVE + CREATE + WRITE na każdym pliku |
| nowy podkatalog z plikiem | CREATE na katalogu, **plik w środku niewidoczny** |
| 50 plików naraz | 100 zdarzeń, nic nie zgubione |

Wnioski do implementacji:
- **fsnotify nie jest rekursywny.** Przy CREATE katalogu trzeba go dodać do watcha i przeskanować, bo pliki mogły powstać, zanim watch się podpiął.
- **Filtr:** ignorować pliki zaczynające się od kropki, kończące się `~`, `.swp`, `.swx` i `.tmp`.
- **Debounce per plik:** zdarzenia jednej operacji przychodzą w oknie ~3ms. Okno 50ms zbiera je w jedno przeładowanie.
- **Własne zapisy serwera też odpalają watcher.** Serwer musi je rozpoznać, np. porównując hash treści z tym, co sam zapisał, inaczej każdy zapis kończy się zbędnym przeładowaniem i eventem.

## S4 - rate limit

Odłożone decyzją użytkownika. Faza 1 i 2 zakładają, że rate limit wygląda jak zwykłe zakończenie kroku z błędem. Właściwa detekcja wraca w Fazie 5.

## Zmiany w requirements

- **42:** hook przekazuje surowy payload harnessu jako body, task z `KANBAN_TASK`, ID w argumentach opcjonalne.
- **44/46:** hooki wstrzykiwane flagą harnessu (`--settings` / `-c`), nigdy plikiem w repo.
- **Nowy:** sesje tmux zarządzane przez aplikację dostają `mouse on` i `focus-events on`.
- **Nowy:** worktree musi być zaufany dla harnessu przed startem agenta.
- **18:** ghostty-web potwierdzony, fallback na xterm.js zbędny.

## Zależności potrzebne w Fazie 1-3

Biblioteka standardowa Go nie ma YAML-a, watchera, pty ani WebSocketu. Każda z tych rzeczy to zależność produkcyjna:

| Zależność | Do czego | Faza |
|---|---|---|
| `go.yaml.in/yaml/v3` | cały model danych | 1 |
| `github.com/fsnotify/fsnotify` | watcher | 1 |
| `github.com/creack/pty` | terminal tmuxa w webie | 3 |
| `github.com/coder/websocket` | terminal tmuxa w webie | 3 |
| `ghostty-web` (vendorowany plik JS + wasm) | terminal w przeglądarce | 3 |
| `htmx` (vendorowany plik JS) | frontend | 3 |

Wybór YAML: `gopkg.in/yaml.v3` stoi od 2022 (v3.0.1). `go.yaml.in/yaml/v3` to jego kontynuacja pod organizacją YAML, to samo API, ostatnie wydanie 2026-07. `goccy/go-yaml` jest alternatywą z innym API, bez przewagi dla tego projektu.
