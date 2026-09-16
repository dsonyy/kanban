# Faza 7 - wynik

Zaimplementowane zgodnie z [plan-faza-7.md](plan-faza-7.md). Nowe: `mirror.go`, `mirror_test.go`. Usunięte: sprawdzanie zaufania z `harness.go`.

## 1. Agent czekający na input przed sesją

- Sprawdzanie zaufania przez czytanie `~/.claude.json` i `~/.codex/config.toml` usunięte w całości.
- Krok z harnessem bez hooka `session-start` po `idle` (domyślnie 20 s) → ACTION REQUIRED z ostatnimi liniami ekranu. `session-start` zdejmuje je eventem `resumed`.
- Na prawdziwym Claude Code w niezaufanym worktree zgłoszenie po 20 s brzmiało: "claude has not started its session after 20s and may be waiting for input: Security guide / ❯ No, exit / Yes, I trust this folder / Enter to confirm · Esc to cancel". Dialogu nie zatwierdzałem, żeby nie dopisywać katalogu tymczasowego do `~/.claude.json`.
- `CLAUDE_CODE_SANDBOXED=1` w `.zshrc` użytkownika dalej działa po stronie Claude Code. Kanban już o tej zmiennej nie wie i nie musi.

## 2. Stan w pamięci

- Lustro plików stanu (`project.yaml`, `board.yaml`, `items/N.yaml`, `items/N.md`) z surową treścią i sparsowaną wartością. Store czyta tylko z niego, sygnatury funkcji bez zmian, więc runner, API i web nie wymagały zmian.
- Reader: watcher wczytuje każdą zmienioną ścieżkę, identyczna treść jest ignorowana. Pełne przeskanowanie przy starcie, przy nowym katalogu i co 30 s wykrywa też usunięcia.
- Writer: jedna funkcja `write`, porównuje dysk z treścią, na której zmiana była liczona. Różnica → lustro bierze wersję z dysku, zmiana nie jest zapisywana, API zwraca 409.
- `update` pracuje na głębokiej kopii stanu, bo funkcje zmian dopisują do slice'ów, a lustro musi zostać wiernym obrazem dysku do chwili udanego zapisu.
- Plik pusty albo niepoprawny zostawia w lustrze ostatni dobry stan z błędem: czytelnicy nadal widzą task, zmiany są blokowane.

Pomiar pod `strace` na projekcie z 2 taskami: 84 otwarcia plików stanu w 7 s przed zmianą, 2 po zmianie (wczytanie przy starcie).

## Weryfikacja

- 27 testów end-to-end i 3 testy pakietu, ~3 min.
- Pełny zestaw z `-race` w procesie testów i w serwerze: zero raportów.
- Nowe testy: `TestAgentWaitingBeforeSession` (pytanie na ekranie → ACTION REQUIRED z jego treścią → `reply` → `resumed` → krok kończy się), `TestExternalEditWinsOverConcurrentChange`, `TestSyncAllCatchesMissedChanges`, `TestHalfWrittenFileKeepsLastGoodState`.
- Celowe psucie kodu, złapane: wyłączone porównanie z dyskiem przed zapisem, pełne przeskanowanie bez wykrywania usunięć.

## Zmiany zachowania

- **Ręczna edycja pliku jest widoczna po wczytaniu przez watcher, zwykle w milisekundach, najpóźniej po 30 s.** Wcześniej była widoczna w tej samej chwili, bo każdy odczyt szedł z dysku. Testy zapisujące plik i od razu czytające API czekają teraz na synchronizację.
- **Zmiana z API w chwili zewnętrznej edycji tego samego pliku kończy się 409** zamiast nadpisania edycji.

## Znane ograniczenia

- Między porównaniem a `rename` zostaje okno rzędu mikrosekund, oznaczone `ponytail:`.
- Logi eventów, przebiegi, artefakty, sugestie, `config.yaml` i `state.yaml` nie są w lustrze. Są dopisywane albo czytane na żądanie i nie biorą udziału w tym wyścigu.
- Runner, który zdążył uruchomić panel, a potem dostał konflikt przy zapisie stanu, zostawia nieprzypisany panel. Sprzątanie zabija go w następnym obrocie, a task jest oceniany od nowa na wersji z dysku.
