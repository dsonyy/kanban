# Faza 5 - wynik

Zaimplementowane zgodnie z [plan-faza-5.md](plan-faza-5.md). Nowe: `harness.go`, `harness_test.go`. Zależność: goldmark.

## Stan

- Kolumna i krok mają `harness: raw | claude | codex`, krok dodatkowo `args` i `resume`.
- Claude Code: kanban nadaje `--session-id`, wstrzykuje hooki przez `--settings ~/kanban/hooks/claude.json`, prompt przechodzi z rozwinięciem `$KANBAN_*`.
- Codex: hooki przez `-c hooks.*`, ID sesji z hooka `SessionStart`, wznowienie przez `codex resume <id>`.
- `kanban hook <zdarzenie> [id]` ≡ `POST /hook/<zdarzenie>/<id>`: `session-start`, `stop`, `notification`, `prompt`, `tool-done`. Na sukcesie nic nie wypisuje, błąd zawsze exit 1.
- `stop` kończy krok. Proces agenta zakończony bez `stop` → `failed`. Spóźnione hooki po zmianie stanu są ignorowane.
- Zaufanie katalogu sprawdzane przed startem, tylko do odczytu, z komendą naprawczą w ACTION REQUIRED.
- Zgubiony panel kroku z sesją → wznowienie sesji zamiast `failed`.
- Widok taska: transkrypt jako markdown z rozwijanymi narzędziami, ID sesji, tokeny.
- Karta działającego zintegrowanego kroku: tokeny i przesuwająca się przerywana ramka (wyłączona przy `prefers-reduced-motion`).
- Koniec kroku zapisuje obok logu terminala `<przebieg>.md` z wycinkiem transkryptu.

## Weryfikacja

`go test ./...`, 19 testów end-to-end, ~80 s. Nowe, z fałszywymi binarkami `claude` i `codex`, które czytają wygenerowaną konfigurację hooków i piszą transkrypty w prawdziwym formacie:

| Test | Sprawdza |
|---|---|
| `TestClaudeIntegration` | plik hooków, tokeny na żywo w stanie i na boardzie, `stop` kończy krok, `goto`, `resume: true` przekazuje poprzednią sesję, transkrypt w HTML (pogrubienie, tabela, narzędzie z outputem), `<przebieg>.md`, spóźniony `stop` po `move` ignorowany i cichy |
| `TestClaudeAttentionAndEarlyExit` | `notification` → ACTION REQUIRED, `tool-done` → `resumed`, wyjście bez `stop` → `failed` |
| `TestUntrustedDirectoryDoesNotStartAgent` | brak zaufania → `failed` z komendą, agent nie wystartował |
| `TestCodexIntegration` | sesja z hooka, transkrypt `response_item`, tokeny z `token_count` |
| `TestHookWithoutServerExitsOne` | bez serwera i przy złych argumentach exit 1, nigdy 2 |
| `TestHookPayloadOverSocket` | payload hooka dostarczony przez socket jest czytany |

Celowe psucie kodu, każde złapane: zdjęta ochrona przed spóźnionym hookiem, wyłączone sprawdzanie zaufania, hook wypisujący odpowiedź, stdin hooka czytany tylko z pipe'a.

Ręcznie, z prawdziwym Claude Code 2.1.273 w `~/repos/random` (zaufane, tryb `plan`, repo po wszystkim bez zmian): sesja i transkrypt w stanie, tokeny rosną w trakcie (34.7k → 39.2k kontekstu), `stop` kończy krok, `goto` do review, `.md` z komendą `ls` i outputem, animowana ramka na boardzie, transkrypt w widoku taska jako markdown.

Z prawdziwym Codeksem 0.154: kanban poprawnie odmówił startu, bo `~/repos/random` nie jest zaufane w `~/.codex/config.toml`. Pełnego przebiegu prawdziwego Codeksa nie robiłem, bo wymagałby zapisania zaufania w twojej konfiguracji.

## Błędy znalezione i naprawione w trakcie

- **Hook z prawdziwego Claude Code dostawał pusty payload.** Claude podaje JSON przez socket, a reguła z Fazy 3 czytała stdin tylko z pipe'a albo pliku. Fałszywe binarki używały pipe'a, więc testy przechodziły. Wyszło dopiero przy prawdziwym agencie. Hook czyta teraz wszystko poza terminalem, a `session-start` bez ID nie kasuje zapisanej sesji. Dołożony test na prawdziwym sockecie failuje bez poprawki.
- **Odpowiedź hooka szła do kontekstu modelu.** Claude Code dokleja stdout niektórych hooków do rozmowy. Hook na sukcesie milczy.
- **Szybki krok nie zostawiał tokenów,** bo runner liczył je tylko w trakcie działania. Liczone też przy `stop`.
- **Surowy krok agenta po kroku z Claude czekałby na hook,** bo harness ostatniej sesji zostaje w stanie dla `resume`. Harness bieżącego kroku jest w osobnym polu.
- **Wskaźniki do elementów rosnącego slice'a w parserze transkryptu** gubiłyby wyniki narzędzi po realokacji. Wyłapane przy przeglądzie przed testami.
- **Rozwinięte narzędzia w transkrypcie zwijały się przy każdej aktualizacji tokenów.** Stan rozwinięcia zachowywany po kluczu.

## Decyzje podjęte w trakcie

- **Zaufanie sprawdzane, nie zapisywane.** Sprawdzone w tej fazie: Claude dziedziczy zaufanie po katalogu nadrzędnym poza katalogiem domowym, Codex przypisuje worktree do głównego repo, a `-c projects...trust_level` nie omija dialogu. Pisanie do `~/.claude.json` równolegle z działającymi agentami groziłoby utratą ich zmian.
- **`stop` kończy krok,** uzasadnienie w planie.
- **Plik hooków Claude'a jest w JSON-ie** mimo punktu 13. To format konfiguracji Claude Code, nie model danych kanbana.

## Znane ograniczenia, świadomie zostawione

- **Rate limit (punkt 43)** - odłożony przez użytkownika.
- **pi** - nie zainstalowane.
- **Agent, który kończy turę pytaniem, kończy krok.** Widoczne w transkrypcie, cofane przez `move`.
- **Wznowienie po zgubionym panelu wysyła "Continue where you left off."** jako nowy prompt.
- **Transkrypt jest parsowany w całości przy każdej zmianie rozmiaru pliku.** Przy kilku agentach i transkryptach po kilka MB bez znaczenia. Przy długich sesjach warto czytać przyrostowo.
- **Pusty `harness:` w starych boardach działa jak `raw`.**

## Dla Fazy 6

- `~/kanban/hooks/` i `~/kanban/worktrees/` muszą trafić do `.gitignore` repo `~/kanban` (auto-commit, punkt 16).
- Domyślny workflow powinien używać `harness: claude` w kolumnach agentów i przypominać o jednorazowym zaufaniu `~/kanban/worktrees`.
