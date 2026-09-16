# Weryfikacja końcowa

Druga, niezależna runda sprawdzania po zamknięciu faz 0-6.

## Co zrobione

1. **Czysty klon:** build, `go vet`, testy.
2. **Wyścigi:** pełny zestaw z serwerem zbudowanym z `-race`. Wcześniej `-race` obejmowało tylko proces testów, a runner, hooki i SSE działają w osobnym procesie serwera. Wynik: zero raportów.
3. **Pierwsze uruchomienie jak użytkownik:** pusty `KANBAN_HOME`, `kanban project new` w kopii prawdziwego repo (`~/repos/random`), domyślny workflow, prawdziwy `claude -p` do wygenerowania skryptu zależności. Przejście todo → planning → worktree i branch → setup → agent, aż do granicy zaufania Claude Code.
4. **Przegląd kodu** pętli runnera, hooków, autoryzacji i historii.

## Błędy znalezione i naprawione

| Błąd | Skutek | Jak wyszedł |
|---|---|---|
| Undo przez `git revert` konfliktowało na dopisywanym logu eventów | undo działało tylko dla ostatniego commita i tylko przez chwilę | test na wolniejszej binarce z `-race` |
| Nieczytelny `123.yaml` wypadał z listy tasków | sprzątanie zabijało sesję działającego agenta | przegląd kodu |
| Pusty `123.yaml` (edytor w trakcie zapisu w miejscu) parsował się do zerowego stanu | runner zapisywał swój stan atomowo i edycja użytkownika przepadała | przegląd kodu, test potwierdził nadpisanie na starym kodzie |
| W domyślnym boardzie ` #!` w zwykłym skalarze YAML zaczynał komentarz | komenda generatora ucięta w połowie cudzysłowu | prawdziwe uruchomienie |
| `--allowedTools` jest wariadyczne i zjadało prompt | `claude -p` bez promptu, generator i sugestie nie działały | prawdziwe uruchomienie |
| Nieudany generator zostawiał pusty `setup.sh` | kolejny `retry` "wykonywał" pusty skrypt i szedł dalej bez setupu | prawdziwe uruchomienie |
| Prompt generatora nie mówił, skąd skrypt jest uruchamiany | skrypt robił `cd` do katalogu projektu kanbana i tam zakładał `.venv` | prawdziwe uruchomienie |
| Zdejmowanie ogrodzenia markdown działało tylko bez tekstu po nim | linia z ```` ``` ```` odpalała interaktywnego `bash`, krok wisiał w nieskończoność | prawdziwe uruchomienie |
| Ręcznie napisany `setup.sh` bez prawa wykonania | "permission denied" | przegląd przebiegu |
| `resolveProject` nie walidował nazwy | nieosiągalne przez HTTP (mux czyści ścieżki), poprawione dla porządku | przegląd kodu |

Każda poprawka poza ostatnią ma test end-to-end sprawdzony w obie strony: failuje na kodzie sprzed poprawki, przechodzi po niej. Testy zależne od czasu dostały szersze okna, bo na wolniejszej binarce fałszywy agent kończył, zanim serwer pokazał stan pośredni.

## Stan

- 26 testów end-to-end, ~3 min, bez osieroconych serwerów tmuxa.
- Pierwsze uruchomienie zatrzymuje się na `failed` z komunikatem: "Claude Code does not trust .../worktrees/1. Run claude once in .../worktrees, accept the trust prompt, then retry." Widoczne w feedzie. Dalej nie szedłem, bo wymagałoby to zapisania zaufania w `~/.claude.json` użytkownika.
- Wygenerowany przez Claude skrypt zależności dla `~/repos/random` padł na tej maszynie, bo systemowy Python 3.14 nie ma `ensurepip`, więc `python -m venv` tworzy środowisko bez pip. To problem maszyny, kanban zgłosił go z właściwym komunikatem.

## Wniosek

Fałszywe agenty w testach sprawdzają mechanikę, ale sześć z dziesięciu błędów wyszło dopiero na prawdziwym Claude Code. Domyślny workflow powinien przejść pełny prawdziwy przebieg (plan, implementacja, PR, review) na repo z remote, zanim będzie traktowany jako gotowy.
