# Faza 0 - spike ryzyk

Cel: rozstrzygnąć cztery rzeczy, które mogą wywrócić architekturę, zanim cokolwiek na nich stanie.
Kod jest jednorazowy i idzie do kosza. Wynikiem fazy są decyzje, nie implementacja.

Wejście: [requirements.md](../../../brain/projects/agent-kanban/requirements.md), [plan.md](../../../brain/projects/agent-kanban/plan.md) faza 0.
Wyjście: `wynik-faza-0.md` z decyzją per spike i listą rzeczy, które zmieniają requirements.

Każdy spike żyje w `spike/<nazwa>/` jako osobny moduł Go. Są niezależne, można je robić równolegle.

## S1 - terminal w przeglądarce (18, 49)

Pytanie: czy Go + pty + `tmux attach` + WebSocket + ghostty-web udźwignie żywy TUI Claude Code.

Kroki:
- serwer Go: pty, `tmux attach -t <sesja>`, most WebSocket dwukierunkowy
- strona z ghostty-web; jeśli nie wstanie bez builda, fallback xterm.js + addon WebGL
- w sesji tmux odpalony Claude Code

Sprawdzam: kolory i pełna ramka TUI, resize okna przeglądarki przepuszczony do pty, scroll i tryb copy tmuxa, skróty (ctrl-c, ctrl-r, strzałki, escape), zachowanie po reconnect WebSocketu, opóźnienie przy dużym outpucie.

Decyzja wychodząca: ghostty-web czy xterm.js, i czy da się bez kroku builda (punkt 18 na tym stoi).

## S2 - hooki Claude Code i Codex (42, 46)

Pytanie: czy hook potrafi zawołać zewnętrzny proces i podać mu ID sesji, oraz ile to kosztuje czasu.

Kroki:
- konfiguracja hooków Stop i Notification wołających skrypt zapisujący na dysk cały payload i env
- to samo dla Codeksa, jeśli ma odpowiednik
- pomiar: ile trwa jedno wywołanie hooka, ile ich leci przy typowej sesji

Sprawdzam: czy ID sesji jest w payloadzie albo env (od tego zależy punkt 46 i `claude --resume`), czy hook blokuje agenta, czy da się odróżnić "czeka na człowieka" od "skończył".

Decyzja wychodząca: zestaw hooków per harness i co dokładnie trafia do `POST /hook/...`.

## S3 - watcher plików w Go (14, 12)

Pytanie: czy fsnotify na Linuksie daje wiarygodny sygnał przy realnych wzorcach zapisu.

Kroki: katalog z plikami YAML i MD, watcher logujący zdarzenia, potem kolejno:
- atomic write (zapis do tmp + rename) - tak pisze serwer
- zapis z Obsidiana
- zapis z vima (backup + rename)
- `git checkout` i `git stash` na całym katalogu
- masowy zapis wielu plików naraz

Sprawdzam: czy zdarzenia się gubią, czy trzeba obserwować katalog zamiast pliku, czy rename wymaga ponownego dodania do watcha, ile trwa debounce, żeby nie przeładowywać stanu pięć razy pod rząd.

Decyzja wychodząca: strategia watcha i okno debounce dla punktu 14.

## S4 - detekcja rate limitu (43)

Pytanie: po czym rozpoznać limit w każdym harnessie.

Kroki: doprowadzić sesję do limitu albo znaleźć zapis takiej sesji, zebrać dla Claude Code i Codeksa: output na stdout, exit code, payload hooka, czy gdzieś jest czas resetu.

Sprawdzam przede wszystkim, czy da się poznać limit bez parsowania tekstu po angielsku, bo parser tekstu zepsuje się przy pierwszej zmianie komunikatu.

Decyzja wychodząca: sygnał per harness i czy czas resetu jest odczytywalny, czy trzeba retry z backoffem.

## Kryterium wyjścia z fazy

Cztery decyzje zapisane w `wynik-faza-0.md`, każda z odpowiedzią na pytanie "co robimy" i notatką, co to zmienia w requirements. Spike'i skasowane albo zostawione w `spike/` bez wpływu na build.
