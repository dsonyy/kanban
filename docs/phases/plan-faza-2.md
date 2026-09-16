# Faza 2 - runner: kolumny, kroki, sesje

Wejście: requirements 25-33, 37, 41-42, 45, 50-51, [wynik-faza-1.md](wynik-faza-1.md).
Wyjście: przykładowy workflow z punktu 32 przechodzi z CLI, z agentem w trybie raw.

## Decyzje

### Składnia kroków w `board.yaml`

Krok to mapa z dokładnie jednym kluczem typu i opcjonalnymi limitami:

```yaml
columns:
  - name: backlog
    steps: []
  - name: todo
    steps:
      - goto: next
  - name: planning
    steps:
      - shell: git -C "$KANBAN_REPO" worktree add "$KANBAN_WORKTREE"
      - agent: claude -p "Write PLAN.md for the task in $KANBAN_TASK_FILE"
        timeout: 1h
        idle: 10m
      - goto: next
  - name: plan-review
    steps:
      - human: Review PLAN.md
      - goto: implementation
```

| Typ | Działanie |
|---|---|
| brak kroków | IDLE, nic się nie dzieje |
| `shell: <cmd>` | komenda w oknie tmuxa taska, exit 0 → następny krok, inaczej `failed` |
| `agent: <cmd>` | jak `shell`, ale zajmuje slot agenta i ma domyślny `idle: 10m` |
| `human: <tekst>` | ACTION REQUIRED, czeka na `approve` albo ręczne przesunięcie |
| `goto: next` / `goto: <kolumna>` | przejście do kolumny i start jej kroków |

Po ostatnim kroku bez `goto` task zostaje w kolumnie jako `done`.

IDLE nie jest osobnym typem kroku, tylko kolumną bez kroków. `human` to IDLE z ACTION REQUIRED i bramką `approve`, bo punkt 32 wymaga, żeby plan review i merge czekały na człowieka, a workflow decydował, dokąd task idzie po zatwierdzeniu.

### Slot agenta per krok, nie per kolumna

Limit agentów (`agents` w `~/kanban/config.yaml`, domyślnie 3) liczy działające kroki `agent`. Task czekający na slot ma status `queued`, kolejka FIFO po czasie zakolejkowania. Kroki `shell` i `human` slotu nie zajmują, więc setup worktree nie blokuje agentów. Kolumna TODO z punktu 32 to zwykłe `goto: next`, a czekanie dzieje się na kroku agenta w PLANNING.

### Pętla rekoncyliacyjna zamiast maszyny zdarzeń

Runner co 500 ms czyta wszystkie taski z dysku i doprowadza świat do stanu z `123.yaml`. Nie ma w pamięci kolejki ani listy procesów. Skutek: restart serwera nic nie gubi (punkt 45), a ręczna edycja stanu jest brana pod uwagę w następnym obrocie.

Jedno `tmux list-panes -a` na obrót zamiast wywołania per task.

### Stan taska w `123.yaml`

```yaml
column: planning
created: 2026-09-16T10:00:00Z
step: 1
status: running        # pending | queued | running | waiting | failed | done
kind: agent            # typ bieżącego kroku, zapamiętany przy starcie
pane: "%12"
started: 2026-09-16T10:01:00Z
attention: ""          # tekst ACTION REQUIRED, pusty gdy nic nie czeka
```

`kind` jest zapisywany przy starcie, bo użytkownik może zmienić `board.yaml` w trakcie kroku, a liczenie slotów nie może się od tego rozjechać.

### tmux: osobny serwer `-L kanban`

- Sesje tasków żyją na osobnym serwerze tmuxa, nie mieszają się z sesjami użytkownika. Jego `~/.tmux.conf` nadal się ładuje.
- Serwer tmuxa dostaje `remain-on-exit on` (exit code przeżywa koniec procesu), `mouse on`, `focus-events on` (punkt 51) i `history-limit 50000`.
- Sesja `kanban-<id>` powstaje przy pierwszym kroku. Okno 0 to shell dla człowieka w katalogu roboczym. Każdy krok dostaje nowe okno.
- Koniec kroku: `capture-pane` całej historii do pliku, potem `kill-pane`.
- Nazwa serwera tmuxa z `KANBAN_TMUX`, żeby testy nie dotykały prawdziwych sesji.

### Zmienne kroku (punkt 28)

| Zmienna | Wartość |
|---|---|
| `KANBAN_TASK` | ID |
| `KANBAN_TASK_FILE` | ścieżka do `123.md` |
| `KANBAN_PROJECT` | nazwa projektu |
| `KANBAN_REPO` | repo z `project.yaml` |
| `KANBAN_WORKTREE` | `~/kanban/worktrees/<id>` |
| `KANBAN_PORT` | `20000 + id` |

Katalog roboczy kroku: `KANBAN_WORKTREE`, jeśli istnieje, inaczej repo. Pierwszy krok tworzący worktree odpala się więc w repo.

`KANBAN_TASK_FILE` i `KANBAN_REPO` dochodzą ponad punkt 28, bo bez nich każdy krok sam musiałby składać ścieżki z `~/kanban`.

### Timeouty (punkt 30)

- `timeout` - twardy limit kroku. Przekroczony: kill, `failed`, ACTION REQUIRED.
- `idle` - brak outputu w panelu przez zadany czas (`window_activity` tmuxa). Krok działa dalej, ale dostaje ACTION REQUIRED. Domyślnie 10m dla `agent`, brak dla `shell`.

### Zapis przebiegu (punkt 29)

Każdy zakończony krok `shell` i `agent` zostawia `items/123.runs/<czas>-<kolumna>-<krok>.log` z pełnym outputem terminala. Event w logu ma exit code, czas trwania i ścieżkę do pliku. Punkt 29 mówi tylko o krokach agenta, ale output testów z kroku `shell` jest równie potrzebny do review, a to ta sama ścieżka kodu.

### Nowe czasowniki

| CLI | REST | Działanie |
|---|---|---|
| `kanban item 123 approve` | `POST /item/123/approve` | krok `human` zaliczony, runner idzie dalej |
| `kanban item 123 retry` | `POST /item/123/retry` | ponów krok, który padł |
| `kanban item 123 attach` | `GET /item/123/attach` | zwraca komendę tmuxa, CLI ją wykonuje (punkt 50) |

`retry` jest potrzebne od razu: bez niego po padnięciu kroku agenta jedyną drogą jest ponowne wejście do kolumny, a to odpali drugi raz `git worktree add` i padnie.

`move` zabija działający krok i startuje kroki nowej kolumny od zera. `archive` zabija całą sesję.

### Zapisy bez wyścigów

Runner i API zmieniają `123.yaml` przez jedną funkcję `update(id, func(*itemState))` pod mutexem store'a. Bez tego runner mógłby nadpisać kolumnę, którą człowiek właśnie zmienił.

## Poza zakresem, świadomie

- **Zaufanie worktree (punkt 52) przechodzi do Fazy 5.** W trybie raw kanban nie wie, że komenda to Claude Code. Dialog zaufania zatrzyma krok, `idle` zgłosi ACTION REQUIRED, człowiek zrobi `attach`.
- **Wznawianie sesji agenta po restarcie tmuxa** (np. po reboocie) to Faza 5, bo wymaga `claude --resume`. W Fazie 2 zgubiona sesja kończy się `failed` z ACTION REQUIRED.
- **Rozgałęzienia i limity pętli `goto`** to Faza 6 (punkt 31).

## Weryfikacja

Test end-to-end na izolowanym serwerze tmuxa, z agentami udawanymi przez komendy shellowe:
1. Pełny workflow z punktu 32: todo → planning (setup worktree, agent pisze plik, zmienne środowiskowe widoczne) → plan-review (ACTION REQUIRED, `approve`) → implementation (agent pada, `retry` przechodzi) → review (`timeout` zabija krok) → merge (`human`, `approve`, sprzątanie worktree) → `done`.
2. Limit agentów 1: drugi task `queued`, rusza po zakończeniu pierwszego.
3. `idle`: działający krok bez outputu dostaje ACTION REQUIRED.
4. Restart: zabicie serwera kanbana w trakcie kroku agenta, ponowny start, krok kończy się i task idzie dalej.
5. `move` w trakcie kroku zabija jego panel.
6. Log eventów ma start i koniec każdego kroku z exit code i czasem, a plik przebiegu zawiera output.

Ręcznie: `kanban item N attach` z prawdziwego terminala, nawigacja po oknach sesji.
