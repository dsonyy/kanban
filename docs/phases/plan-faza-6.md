# Faza 6 - funkcje self-contained

Wejście: requirements 16, 31-33, 39-40, [wynik-faza-5.md](wynik-faza-5.md).
Sześć niezależnych części. Kolejność wynika tylko z tego, że sugestie potrzebują grafu, a domyślny workflow korzysta ze skryptu zależności.

## 1. Katalog taska na artefakty

Plan i review z punktu 32 muszą gdzieś leżeć. W worktree agent by je zacommitował do brancha. Każdy krok dostaje `KANBAN_TASK_DIR` = `items/123.files/`, tworzony przy starcie kroku. Widok taska listuje pliki z tego katalogu, markdown renderuje.

## 2. Skrypt zależności (punkt 33)

Nowy typ kroku `setup: true`. Skrypt leży w projekcie kanbana (`projects/<nazwa>/setup.sh`), nie w repo, i jest dostępny jako `KANBAN_SETUP`.

- Skrypt istnieje → krok działa jak `shell: "$KANBAN_SETUP"`.
- Skryptu brak → ACTION REQUIRED "No setup script. Approve to let an agent write it."
- `approve` → kanban uruchamia `setup.generate` z `board.yaml` jako zwykły krok w tmuxie. Po sukcesie zdejmuje z wyniku ogrodzenie markdown, jeśli agent je dodał, ustawia prawo wykonania i uruchamia skrypt w tym samym kroku.

```yaml
setup:
  generate: claude -p "..." > "$KANBAN_SETUP"
```

## 3. Domyślny workflow (punkt 32)

`kanban project new` zapisuje board z punktu 32 zamiast czterech pustych kolumn: backlog, todo (`goto`), planning (worktree, setup, agent pisze plan do `$KANBAN_TASK_DIR/PLAN.md`), plan-review (`human`), implementation (agent z `resume`, testy, commit, PR przez `gh`), review (inny agent pisze `$KANBAN_TASK_DIR/REVIEW.md`), merge (`human`, sprzątanie worktree), done.

Kroki tworzące worktree i branch są idempotentne, żeby `retry` po padnięciu dalszego kroku nie wywracał się na istniejącym worktree.

## 4. Rozgałęzienia (punkt 31)

```yaml
- shell: npm test
  on_fail: implementation
  max_loops: 3
```

- Krok padający (exit ≠ 0, timeout, agent wychodzi bez `stop`) z `on_fail` → przejście do kolumny, event `moved` z `message: on_fail`.
- Licznik per krok w stanie taska. Po `max_loops` (domyślnie 3) zwykłe `failed` z ACTION REQUIRED.
- `goto` też ma licznik: ponad 20 przejść bez akcji człowieka → `failed` "goto loop".
- Liczniki zeruje akcja człowieka (`move`, `retry`, `approve`).

## 5. Historia w git (punkt 16)

- Przy starcie serwer robi z `~/kanban` repo git z `.gitignore` na socket, lock, token, `hooks/` i `worktrees/`.
- Co 2 s runner commituje, jeśli `git status` coś pokazuje. Jeden commit zbiera zmiany z całego okna. Autor ustawiany flagą, niezależny od globalnej konfiguracji gita.
- `kanban history` ≡ `GET /history`: ostatnie 50 commitów z plikami. `kanban history <hash> undo` ≡ `POST /history/<hash>/undo`: `git revert`. Konflikt → 400 i `revert --abort`.
- UI: sekcja historii w settings z przyciskiem Undo przy commicie.

Undo przywraca pliki, a runner reaguje na pliki: cofnięcie przesunięcia taska odpali kroki starej kolumny. To konsekwencja zasady "dysk jest prawdą", świadomie nie obchodzona.

## 6. Graf (punkt 39)

- `123.yaml` dostaje `parents: [10, 11]`. Dzieci są wyliczane.
- `kanban item 12 link 10` / `unlink 10` ≡ `POST /item/12/link/10`. ID globalne, więc rodzic może być w innym projekcie.
- `kanban project demo graph` ≡ `GET /project/demo/graph`: węzły i krawędzie.
- Tab `graph`: SVG renderowany po stronie serwera. Warstwy wg najdłuższej ścieżki od korzeni, w warstwie kolejność po ID, krawędzie proste. Bez biblioteki.
- Relacje nic nie blokują i nie pokazują się na boardzie.

## 7. Sugestie następnych kroków (punkt 40)

- `kanban item 12 suggest` ≡ `POST /item/12/suggest`. Rodzina = task, jego przodkowie i potomkowie.
- Kanban zapisuje rodzinę jako markdown do `items/12.family.md` i uruchamia w tle `suggest.command` z `board.yaml` z `KANBAN_FAMILY_FILE` i `KANBAN_SUGGESTIONS` (`items/12.suggestions.yaml`). Po zakończeniu event `suggested` albo `failed`.
- Widok taska pokazuje sugestie z przyciskiem Accept. `kanban item 12 accept <n>` ≡ `POST /item/12/accept/<n>`: nowy task z treścią sugestii, `parents: [12]`, w kolumnie `suggest.to`, i od razu rusza jej kroki. Sugestia znika z pliku.

```yaml
suggest:
  to: implementation
  command: claude -p "$(cat "$KANBAN_FAMILY_FILE") ..." > "$KANBAN_SUGGESTIONS"
```

Komenda działa poza tmuxem i poza limitem agentów. To jednorazowe zapytanie bez interakcji, oznaczone `ponytail:`.

## Weryfikacja

Testy end-to-end, po jednym na część, z fałszywymi komendami tam, gdzie domyślnie działałby Claude:
- artefakt w `KANBAN_TASK_DIR` widoczny i wyrenderowany w widoku taska
- setup: brak skryptu → ACTION REQUIRED → approve → generator (z ogrodzeniem markdown) → skrypt wykonany w worktree, drugi task używa go od razu
- nowy projekt ma board z punktu 32, który przechodzi walidację
- `on_fail` wraca do kolumny, po `max_loops` `failed`, `move` zeruje licznik, pętla `goto` zatrzymana
- auto-commit, historia z plikami, undo przywraca plik i runner na to reaguje, token nie trafia do repo
- link/unlink, graf z krawędziami, SVG na stronie, cykl nie zawiesza layoutu
- suggest z fałszywą komendą, accept tworzy dziecko w kolumnie docelowej i odpala jej kroki

Ręcznie w Chrome: graf, historia z undo, sugestie w widoku taska.
