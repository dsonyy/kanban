# Faza 5 - integracje z harnessami

Wejście: requirements 29, 41-48, 52, [wynik-faza-4.md](wynik-faza-4.md), S2 z [wynik-faza-0.md](wynik-faza-0.md).
Wyjście: krok z Claude Code albo Codeksem kończy się, gdy agent skończył turę, a nie na heurystyce. Transkrypt, zużycie tokenów i sesja są widoczne przy tasku.

## Zakres

W zakresie: Claude Code i Codex, oba zainstalowane.

Poza zakresem:
- **Rate limit (punkt 43)** - odłożony decyzją użytkownika w Fazie 0.
- **pi** - nie jest zainstalowane, nie ma na czym sprawdzić integracji. Model integracji zostaje otwarty na kolejny harness.

## Decyzje

### Składnia

```yaml
columns:
  - name: implementation
    harness: claude              # domyślny harness kroków agent w kolumnie (punkt 44)
    steps:
      - agent: Implement PLAN.md. The task is in $KANBAN_TASK_FILE.
        args: [--permission-mode, acceptEdits]
        resume: true             # kontynuuj ostatnią sesję taska zamiast nowej
      - agent: npm test          # harness: raw nadpisuje kolumnę
        harness: raw
```

- `harness: raw` (domyślny) - `agent` to komenda shellowa, jak w Fazie 2.
- `harness: claude` / `codex` - `agent` to prompt. Kanban składa komendę, wstrzykuje hooki i nadaje sesję.

### Harness jako trzy funkcje, bez interfejsu

Dla każdego harnessu: zbuduj komendę, sprawdź zaufanie katalogu, przeczytaj transkrypt. Dwa harnessy to `switch`, nie interfejs z rejestrem.

| | Claude Code | Codex |
|---|---|---|
| Komenda | `claude --settings ~/kanban/hooks/claude.json --session-id <uuid> [--resume <id>] <args> "<prompt>"` | `codex --dangerously-bypass-hook-trust -c hooks.X=... [resume <id>] <args> "<prompt>"` |
| ID sesji | nadawane przez kanban (`--session-id`) | z hooka `SessionStart` |
| Zaufanie | `~/.claude.json`: katalog roboczy albo jego rodzic (poza katalogiem domowym) ma `hasTrustDialogAccepted` | `~/.codex/config.toml`: repo główne worktree ma `trust_level = "trusted"` |
| Transkrypt | `transcript_path` z hooka, JSONL `user`/`assistant` | `transcript_path` z hooka, JSONL `response_item` |

Plik hooków Claude'a generowany przy starcie serwera z bezwzględną ścieżką do binarki kanbana.

### Zaufanie sprawdzane przed startem, tylko do odczytu

Oba harnessy blokują start dialogiem zaufania w nowym katalogu, a hooki wtedy jeszcze nie działają, więc kanban nic by nie zobaczył. Sprawdzone w tej fazie:
- Claude dziedziczy zaufanie z katalogu nadrzędnego (poza katalogiem domowym). Jedno zaufanie dla `~/kanban/worktrees` pokrywa wszystkie worktree.
- Codex przypisuje zaufanie worktree do głównego repo projektu. Jedno zaufanie na projekt.
- `-c projects."<ścieżka>".trust_level` w Codeksie nie omija dialogu.

Kanban czyta konfigurację harnessu i przy braku zaufania nie startuje kroku: `failed` z ACTION REQUIRED podającym dokładną komendę do jednorazowego uruchomienia. Kanban nigdy nie zapisuje `~/.claude.json` ani `~/.codex/config.toml`, bo działające agenty nadpisują te pliki i równoległy zapis gubiłby ich zmiany.

### Hooki (punkt 42)

`kanban hook <zdarzenie> [id]` ≡ `POST /hook/<zdarzenie>/<id>`. Bez `id` CLI bierze `KANBAN_TASK` i wstawia do ścieżki, więc reguła 1:1 trzyma. Body to surowy JSON z harnessu. Błąd hooka kończy się exit 1, nigdy 2, bo exit 2 blokuje agenta w Claude Code.

| Zdarzenie | Działanie |
|---|---|
| `session-start` | zapis ID sesji i ścieżki transkryptu w stanie taska |
| `stop` | krok zakończony, jak exit 0 w trybie raw |
| `notification` | ACTION REQUIRED z komunikatem harnessu |
| `prompt` / `tool-done` | ACTION REQUIRED znika, event `resumed` |

Hook działa tylko na task w stanie `running` z harnessem, inaczej jest ignorowany z kodem 200. Hooki spóźnione po `move` nie mogą zmienić nowej kolumny.

**Stop kończy krok.** Agent, który zatrzyma się z pytaniem, też kończy krok. Alternatywa (agent sam woła `kanban item N finish`) wymaga, żeby każdy prompt pamiętał o instrukcji, a zapomniany prompt wisi w nieskończoność. Przy obecnym wyborze błąd jest widoczny w transkrypcie i łatwy do cofnięcia przez `move`.

Proces agenta, który zakończył się bez `stop` (np. człowiek wyszedł z TUI), kończy krok jako `failed`.

`idle` jest domyślnie wyłączony dla harnessów z integracją, bo stan przychodzi z hooków. Jawne `idle` w kroku dalej działa.

### Sesje (punkty 45-46)

- Stan taska dostaje `harness`, `session`, `transcript`. Zostają po zakończeniu kroku, więc `resume: true` w kolejnym kroku kontynuuje tę samą rozmowę.
- Zgubiony panel (np. restart tmuxa) przy harnessie z sesją: krok startuje ponownie ze wznowieniem sesji zamiast `failed`.

### Transkrypt i zapis przebiegu (punkty 29, 47)

- Parser obu formatów do wspólnej listy: rola, tekst, wywołania narzędzi z wynikiem.
- Widok taska: sekcja "Transcript" dla ostatniej sesji, tekst renderowany jako markdown (goldmark, bez surowego HTML), narzędzia jako zwijane bloki.
- Koniec kroku zapisuje obok logu terminala `<przebieg>.md` z wycinkiem transkryptu od startu kroku. Review idzie z tego pliku.

### Tokeny i ramka (punkt 48)

- Runner przy każdym obrocie dla działających kroków z harnessem sprawdza rozmiar transkryptu i tylko przy zmianie przelicza: kontekst ostatniego wywołania i sumę tokenów wyjściowych. Wynik w stanie taska, więc board odświeża się przez SSE.
- Karta takiego kroku: `ctx 36k · out 1.2k` i animowana przerywana ramka.

## Weryfikacja

Testy end-to-end z fałszywymi binarkami `claude` i `codex` w `PATH` testu. Skrypty wołają `kanban hook` z payloadem w formacie prawdziwych harnessów i dopisują linie do transkryptu w prawdziwym formacie. Testy nie zależą od sieci ani kont.

- Claude: komenda ma `--settings`, `--session-id` i prompt z rozwiniętymi zmiennymi, `stop` kończy krok, transkrypt w widoku taska jako HTML, `<przebieg>.md` zapisany, tokeny w stanie w trakcie kroku
- `notification` → ACTION REQUIRED, `prompt` → `resumed`
- `resume: true` → `--resume <poprzednia sesja>`
- brak zaufania → `failed` z komendą w komunikacie, fałszywy agent nie został uruchomiony
- proces agenta kończy się bez `stop` → `failed`
- spóźniony `stop` po `move` nie zmienia stanu
- Codex: ID sesji z `session-start`, transkrypt w formacie `response_item`, `stop` kończy krok
- `kanban hook stop` bez serwera → exit 1, nie 2

Ręcznie: prawdziwy Claude Code z krótkim promptem w demo (zaufany katalog), board z animowaną ramką i tokenami, transkrypt w widoku taska. Prawdziwy Codex tak samo.
