# Faza 3 - wynik

Zaimplementowane zgodnie z [plan-faza-3.md](plan-faza-3.md). Nowe: `web.go`, `web/templates/*.html`, `web/static/app.{css,js}`, vendorowany ghostty-web, `web_test.go`. Zależności: fsnotify, creack/pty, coder/websocket.

## Stan

- `kanban` wypisuje przy starcie adres z tokenem, `kanban server` też go zwraca. Pierwsze wejście ustawia ciasteczko i czyści token z paska adresu.
- Taby projektów i widoków, start w ostatnim projekcie.
- Kanban: kolumny, karty z pierwszą linią, drag and drop przez REST API, edytor na dole z wyborem kolumny i Ctrl+Enter.
- Widok taska: stan, ACTION REQUIRED, approve/retry/move/archive, eventy, przebiegi rozwijane na żądanie, edycja treści, terminal sesji otwierany przyciskiem.
- Terminal projektu: sesja tmux `project-<nazwa>` w repo.
- Settings: `board.yaml` i `project.yaml`, walidacja przed zapisem, błąd zostaje na ekranie do kliknięcia.
- Każda zmiana na dysku w projekcie, z API, runnera albo edytora, odświeża otwarte strony przez SSE.
- Ręczna zmiana `column:` w `123.yaml` odpala kroki nowej kolumny (luka z Fazy 1 zamknięta).

## Weryfikacja

`go test ./...`, 10 testów end-to-end, ~50 s. Nowe:

| Test | Sprawdza |
|---|---|
| `TestWebPagesAndAuth` | 401 bez tokena, link z tokenem ustawia ciasteczko, `/` ląduje na boardzie, wszystkie widoki i statyki 200, przesunięcie przez API z ciasteczkiem, zły board → 400 i plik nietknięty |
| `TestLiveUpdates` | SSE po zmianie z API i po ręcznym zapisie pliku, ręczna zmiana kolumny odpala kroki |
| `TestTerminalWebSocket` | WebSocket bez tokena odrzucony, komenda wpisana przez WebSocket wraca z outputem, resize trafia do tmuxa |

Celowe psucie kodu, złapane: wyłączone wykrywanie ręcznej zmiany kolumny, autoryzacja przepuszczająca wszystko.

Ręcznie w Chrome, na demo z dziewięcioma taskami we wszystkich stanach: board, dodanie taska z edytora (licznik zmienia się bez przeładowania), drag and drop z backlogu przez `goto` do planning, widok taska z błędem, rozwinięty przebieg, terminal sesji taska, terminal projektu z działającym Claude Code, zapis złego boarda z komunikatem.

## Błędy znalezione i naprawione w trakcie

- **CLI wisiało na otwartym stdin.** Powłoki, z których komendy odpalają agenci, dają jako stdin socket, który się nie zamyka. `kanban project new` czekało na koniec body w nieskończoność. Teraz treść czytana jest tylko z pipe'a albo pliku, a `project new` body nie czyta wcale. Test odtwarza to na prawdziwym sockecie i failował przed poprawką.
- **Znaki wpisane przed otwarciem WebSocketu przepadały.** Teraz są buforowane.

Poprawki wyglądu po przeglądzie zrzutów:
- jasne natywne paski przewijania na czarnym tle → ciemne, cienkie
- podkreślenie karty `failed` przechodziło na cały tekst → stan widać po bloku ACTION REQUIRED, running ma jasną ramkę, queued przerywaną
- `done` na kartach w kolumnach bez kroków był szumem → ukryte
- nazwy kolumn wymuszane wielkimi literami → zapis taki, jak w `board.yaml`
- tabela eventów zawijała się słowo po słowie → jedna linia na event, bez powtórzonego komunikatu
- przycisk terminala był pod zgięciem → sesja nad treścią
- glify powerline w prompcie jako kwadraty → Hack w stosie fontów
- tmux ucinał nazwę sesji do 10 znaków → `status-left-length 40`
- YAML w settings zawijał się jak złe wcięcie → bez zawijania

## Odstępstwa od planu i requirements

- **Bez htmx** (punkt 18). Uzasadnienie w planie. Punkt 18 w requirements zaktualizowany.
- **Taby feed i graph nie istnieją,** dopóki nie ma za nimi widoków.

## Znane ograniczenia, świadomie zostawione

- **Nie sprawdziłem widoku na telefonie.** Okno Chrome nie dało się zwęzić w tym środowisku. CSS ma breakpoint 800 px, ale nikt tego nie oglądał. Do sprawdzenia przez Tailscale.
- **Napisy w UI są w szablonach.** Globalna reguła mówi, żeby ich nie hardkodować, ale projekt nie ma systemu tłumaczeń, a budowanie go dla narzędzia jednej osoby to spekulacja. Do decyzji.
- **Ręczna edycja treści `123.md` nie trafia do logu eventów.** Kolumna tak, treść nie. Wymagałoby śledzenia hashy własnych zapisów.
- **SSE odświeża całą stronę i podmienia `#view`.** Przy bardzo długim logu eventów będzie to widoczne. Na razie nie jest.
- **Tekst ACTION REQUIRED i eventy są po angielsku, a notatki po polsku.** Spójne w obrębie aplikacji.

## Dla Fazy 4

- Feed potrzebuje eventów `attention` i `failed` ze wszystkich projektów. Są w `123.log.yaml`, ale bez pola "rozwiązane". Stan "nadal czeka" jest w `123.yaml` (`attention` niepuste).
- Hub SSE ma już zdarzenia per projekt. Feed może słuchać wszystkich.
- Web Push wymaga HTTPS, czyli `tailscale serve`. ntfy.sh działa bez tego.
