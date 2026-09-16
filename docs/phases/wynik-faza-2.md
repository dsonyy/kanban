# Faza 2 - wynik

Zaimplementowane zgodnie z [plan-faza-2.md](plan-faza-2.md). Nowy plik `runner.go`, zmiany w `store.go`, `server.go`, `grammar.go`, `main.go`.

## Stan

- Runner co 500 ms doprowadza tmuxa do stanu z `123.yaml`. Serwer może paść w dowolnym momencie: po restarcie działające kroki są podejmowane tam, gdzie były.
- Kroki `shell`, `agent`, `human`, `goto` ze składnią z planu. Limity `timeout` i `idle`.
- Limit agentów z `~/kanban/config.yaml`, kolejka FIFO.
- Sesje na osobnym serwerze tmuxa (`-L kanban`), okno 0 to shell, każdy krok w oknie `step`.
- Sprzątanie jest poziomowe: panel kroku, do którego nie odwołuje się żaden task, jest zabijany. Sesja taska zarchiwizowanego albo nieistniejącego też. Dzięki temu `move`, `archive`, `retry` i awarie w połowie startu kroku nie potrzebują osobnego kodu sprzątającego.
- Zakończony albo zabity przez timeout krok zostawia `items/123.runs/<czas>-<kolumna>-<krok>.log`. Event w logu ma exit code, czas trwania i ścieżkę do pliku.
- `approve`, `retry`, `attach` działają. `attach` działa również z wnętrza innej sesji tmuxa.

## Weryfikacja

`go test ./...`, 7 testów end-to-end na prawdziwej binarce i izolowanym serwerze tmuxa, ~50 s:

| Test | Sprawdza |
|---|---|
| `TestWorkflow` | pełny workflow z punktu 32 z agentami udawanymi przez shell: goto, setup worktree, zmienne środowiskowe, ACTION REQUIRED, approve, padnięcie i retry, timeout, sprzątanie worktree, log eventów, pliki przebiegów, brak osieroconych paneli |
| `TestAgentLimitQueue` | limit 1: drugi task czeka, rusza po pierwszym |
| `TestIdleAttention` | brak outputu → ACTION REQUIRED bez zabijania kroku |
| `TestRestartKeepsRunningStep` | kill serwera w trakcie kroku, restart, krok kończy się czysto i task idzie dalej |
| `TestMoveKillsRunningStep` | `move` w trakcie kroku zabija jego panel |
| `TestBrokenBoardDoesNotKillRunningStep` | zepsuty `board.yaml` nie zabija działającego kroku, task czeka z ACTION REQUIRED i rusza po naprawie |
| `TestEndToEnd` | cała Faza 1 bez regresji |

Celowe psucie kodu, każde złapane przez test:
- wyłączone zabijanie paneli → `TestMoveKillsRunningStep`
- wyłączony limit agentów → `TestAgentLimitQueue`
- zdjęta ochrona działającego kroku przed zepsutym boardem → `TestBrokenBoardDoesNotKillRunningStep`

Ręcznie: `kanban item 1 attach` z wnętrza innej sesji tmuxa, przełączenie na okno `step`, output agenta widoczny na żywo, po zakończeniu okno znika, a przebieg jest w pliku.

## Decyzje podjęte w trakcie

- **Zepsuty `board.yaml` nie zatrzymuje działającego kroku.** Obsidian i edytory zapisują w trakcie pisania, więc chwilowo niepoprawny YAML jest normalny. Działający krok jest dalej obserwowany. Task, który ma wystartować następny krok, czeka z ACTION REQUIRED i rusza sam po naprawie boarda. Pierwsza wersja oznaczała taki task jako `failed`, a sprzątanie zabijało agenta. Wyłapane przy przeglądzie kodu przed testami.
- **Kroki `shell` też zostawiają plik przebiegu,** nie tylko `agent`. Ta sama ścieżka kodu, a output testów jest potrzebny do review.

## Znane ograniczenia, świadomie zostawione

- **Zabity panel nie zostawia pliku przebiegu przy `move`.** Output kroku przerwanego ręcznie przepada. Log eventów ma samo przesunięcie.
- **Przejście między krokami trwa do 500 ms.** Workflow z pięcioma krokami startuje w ~2,5 s. Dla agentów pracujących minutami bez znaczenia.
- **`goto` bez limitu pętli.** Kolumna robiąca `goto` sama do siebie kręci się co 500 ms i dopisuje eventy. Limity pętli to Faza 6 (punkt 31).
- **Po `attach` i zakończeniu kroku tmux przeskakuje na okno 0,** bo okno kroku znika. Tak działa tmux.
- **Zaufanie worktree dla Claude Code (punkt 52)** i wznawianie sesji agenta po restarcie tmuxa czekają na Fazę 5.

## Dla Fazy 3

- Watcher (przeniesiony z Fazy 1): runner zapisuje `123.yaml` tylko przy faktycznej zmianie stanu, więc watcher nie dostanie dwóch zapisów na sekundę na każdy task.
- `ensureSession` już istnieje. Terminal w webie to `tmux -L kanban attach -t =kanban-<id>` przez pty, dokładnie jak w spike'u S1.
- Widok taska potrzebuje: `GET /item/N`, `GET /item/N/log` i treści plików z `123.runs/`. Tego ostatniego nie ma jeszcze w API.
