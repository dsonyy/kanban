# Faza 1 - wynik

Zaimplementowane zgodnie z [plan-faza-1.md](plan-faza-1.md). Kod: `main.go` (CLI), `grammar.go` (wspólna gramatyka), `store.go` (dysk), `server.go` (HTTP), `e2e_test.go`.

## Stan

- `kanban` bez argumentów stawia serwer na pierwszym planie: flock, socket 0600, TCP z tokenem. Drugie uruchomienie wypisuje PID działającego serwera i kończy z exit 0.
- `kanban <argumenty>` idzie po sockecie. Bez serwera exit 1 i komunikat. Błędna gramatyka exit 2 bez łączenia się z serwerem.
- Gramatyka i ścieżki dokładnie jak w tabeli w planie. Kolejność par zasobów dowolna.
- ID tasków globalne, nadawane pod mutexem.
- Stdin czytany tylko dla czasowników z treścią (`new`, `edit`). Agent wołający `move` z otwartym pustym stdin nie zawiesza CLI.

## Weryfikacja

`go test ./...` - jeden test end-to-end na prawdziwej binarce i prawdziwym sockecie, pokrywa wszystkie punkty z sekcji "Weryfikacja" w planie.

Sprawdzone, że test łapie błędy, przez celowe psucie kodu:
- wyłączona weryfikacja tokena → fail `tcp without token: 200`
- nadawanie ID o jeden za nisko → fail `missing "id: 1"`

Ręcznie, poza testem:
- 20 równoległych `item new` → 21 plików, ID 1-21 bez duplikatów
- `sleep 30 | kanban item 1 move doing` wraca od razu
- `sed` na `1.yaml` widoczny w następnym `kanban item 1`

## Odstępstwa od planu

- Watcher przesunięty do Fazy 3, uzasadnienie w planie. `plan.md` zaktualizowany.
- `GET /server` zwraca PID. To nowy zasób, potrzebny do komunikatu "już działa".

## Znane ograniczenia, świadomie zostawione

- **Zewnętrzna edycja nie trafia do logu eventów.** Ręczna zmiana kolumny w `123.yaml` działa, ale log jej nie widzi. Rozwiąże się w Fazie 3 razem z watcherem.
- **Pliki tworzone przez serwer mają uprawnienia 0600** (skutek `os.CreateTemp`). Dla jednego użytkownika bez znaczenia.
- **Jeden globalny mutex na zapisy**, oznaczony `ponytail:`. Wystarcza przy jednym użytkowniku i kilku agentach.
- **Nadawanie ID skanuje wszystkie projekty.** Przy tysiącach tasków to kilka ms, niezauważalne.

## Dla Fazy 2

- Kroki kolumn są w `board.yaml` jako `steps: []` i nie są czytane. Składnia kroków to pierwsza decyzja Fazy 2.
- `store.moveItem` to jedyne miejsce zmiany kolumny, więc runner podpina się tutaj.
- Log eventów ma otwarte pola `event/from/to`. Runner dopisze kroki, exit code i czasy (punkt 17).
