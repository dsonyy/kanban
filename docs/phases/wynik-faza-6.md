# Faza 6 - wynik

Zaimplementowane zgodnie z [plan-faza-6.md](plan-faza-6.md), z jednym odstępstwem przy undo opisanym niżej. Nowe: `workflow.go`, `history.go`, `graph.go`, `web/templates/graph.html`, `phase6_test.go`.

## Stan

1. **Katalog artefaktów:** `KANBAN_TASK_DIR` = `items/123.files/`. Pliki widoczne w widoku taska, markdown renderowany.
2. **Skrypt zależności:** krok `setup: true`. Brak `setup.sh` → ACTION REQUIRED → `approve` → `setup.generate` → ogrodzenie markdown zdjęte, prawo wykonania, skrypt uruchomiony w tym samym kroku. Kolejne taski używają go od razu.
3. **Domyślny workflow:** `kanban project new` zapisuje board z punktu 32 z komentarzem na górze. Claude w planning, implementation i review, plan i review w `$KANBAN_TASK_DIR`, PR przez `gh`, idempotentne tworzenie worktree.
4. **Rozgałęzienia:** `on_fail` + `max_loops` (domyślnie 3) dla wyjścia ≠ 0, timeoutu i agenta bez `stop`. Limit 20 przejść `goto` bez akcji człowieka. Akcje człowieka zerują liczniki.
5. **Historia:** `~/kanban` jest repo git, auto-commit co 2 s, `kanban history`, `kanban history <hash> undo`, sekcja w settings z przyciskiem Undo.
6. **Graf:** `parents` w `123.yaml`, `link`/`unlink`, `kanban project X graph`, tab graph z SVG z serwera, relacje i formularz w widoku taska.
7. **Sugestie:** `suggest`, `suggestions`, `accept <n>`, rodzina jako markdown, komenda w tle, zaakceptowana sugestia staje się dzieckiem w kolumnie `suggest.to` i od razu rusza.

## Weryfikacja

`go test ./...`, 24 testy end-to-end, ~125 s, bez osieroconych serwerów tmuxa. Nowe:

| Test | Sprawdza |
|---|---|
| `TestDefaultBoardAndTaskFiles` | domyślny board ma kolumny z punktu 32 i przechodzi walidację, plik z `KANBAN_TASK_DIR` wyrenderowany w widoku taska |
| `TestSetupScriptGeneration` | ACTION REQUIRED, approve, generator z ogrodzeniem, skrypt czysty i wykonywalny, drugi task bez pytania |
| `TestOnFailLoopsAndGotoLimit` | dokładnie `max_loops` powrotów, potem `failed`, `move` zeruje licznik, pętla `goto` zatrzymana |
| `TestHistoryAndUndo` | auto-commit, token/socket/hooki nieśledzone, poprawny komunikat commita, undo przywraca treść i kolumnę, log eventów nietknięty, zły hash → 400 |
| `TestGraphAndSuggestions` | link/unlink, odrzucenie siebie i nieistniejącego rodzica, głębokość i krawędzie, cykl nie zawiesza grafu, SVG na stronie, sugestie z rodziny, accept tworzy dziecko, które rusza w kolumnie docelowej |

Celowe psucie kodu, każde złapane: limit `on_fail`, limit `goto`, zdejmowanie ogrodzenia, odrzucanie linku do siebie, zachowanie logów przy undo, komunikat commita.

Ręcznie w Chrome na demo: graf z 10 węzłami, sugestie wygenerowane z przycisku i zaakceptowana jako #14 z rodzicem #9 (relacje i lista odświeżone bez przeładowania), historia z Undo w settings.

## Błędy znalezione i naprawione w trakcie

- **Undo cofało log eventów.** Log jest w repo, więc `git revert` usuwał wpisy. Undo robi teraz revert bez commita, przywraca z HEAD `*.log.yaml` i `*.runs/`, i dopiero commituje. Po drodze drugi błąd: git odrzuca całą komendę `checkout`, gdy jeden z pathspeców nic nie dopasuje, więc każdy pathspec idzie osobno.
- **Komunikaty commitów traciły pierwszą literę** ("tate.yaml"). `TrimSpace` na outpucie `git status --porcelain` zjadał wiodącą spację pierwszej linii. Wyłapane na zrzucie ekranu, test dopisany. Commity z uciętymi komunikatami sprzed poprawki zostają w historii demo.
- **Krawędzie grafu się krzyżowały,** bo kolejność w warstwie szła po ID. Teraz po średniej pozycji rodziców, węzły bez relacji na końcu.
- **Własna funkcja kopiowania mapy** zamiast `maps.Clone` z biblioteki standardowej. Usunięta.

## Odstępstwo od planu

**Undo przywraca pełny stan taska, a nie odpala kroków starej kolumny.** Plan zakładał, że cofnięte przesunięcie wygląda dla runnera jak ręczna zmiana kolumny. W praktyce revert przywraca też pole `ran`, więc task wraca do dokładnie tego stanu, w którym był. Spójniejsze niż plan: undo to powrót do migawki, nie nowa akcja. Jeśli migawka miała działający krok, runner zauważy brak panelu i zgłosi to jak każdy zgubiony krok.

## Znane ograniczenia, świadomie zostawione

- **Sugestie działają poza tmuxem i limitem agentów,** oznaczone `ponytail:`.
- **Undo commita, który utworzył task, zostawia jego `.log.yaml`.** Task znika z boardu, log zostaje na dysku.
- **Auto-commit grupuje zmiany z 2 s.** Undo cofa cały taki commit, czyli czasem ruch runnera razem z akcją człowieka.
- **Domyślny workflow nie był uruchomiony end-to-end z prawdziwym Claude'em i `gh`.** Wymagałby prawdziwego repo z remote i utworzenia PR. Każdy jego mechanizm jest pokryty testem osobno.
- **Graf pokazuje tylko relacje z dziećmi w bieżącym projekcie.** Rodzic z innego projektu jest widoczny jako węzeł przerywany, dziecko w innym projekcie nie.
