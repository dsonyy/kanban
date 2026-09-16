# Faza 3 - web: shell i kanban

Wejście: requirements 3-4, 10, 14-15, 18-21, 23-25, 36, 49, 51, [wynik-faza-2.md](wynik-faza-2.md), wnioski S1 i S3 z [wynik-faza-0.md](wynik-faza-0.md).
Wyjście: praca z kilkoma agentami z przeglądarki, bez otwierania zwykłego terminala.

## Decyzje

### Bez htmx, natywne EventSource i fetch

Punkt 18 zakłada htmx. Rezygnuję z niego z dwóch powodów:
- UI ma korzystać z tego samego REST API co CLI, a API przyjmuje surowe body (treść taska, cały `board.yaml`). Formularze htmx wysyłają `form-urlencoded`, więc i tak trzeba by pisać JS do każdego zapisu albo drugie API pod UI.
- Odświeżanie po SSE to jedna generyczna funkcja: pobierz tę samą stronę, podmień `#view`. Rozszerzenie SSE do htmx robi to samo, tylko jako druga zależność.

Zostaje: Go templates renderujące pełne strony, jeden `app.js` (~100 linii) i zero kroku builda. Jeśli stan po stronie klienta zacznie boleć, htmx albo framework dochodzą później, zgodnie z punktem 18.

### Adresy

| Ścieżka | Co |
|---|---|
| `/` | przekierowanie do ostatniego projektu |
| `/ui/<projekt>` | kanban |
| `/ui/<projekt>/item/<id>` | widok taska |
| `/ui/<projekt>/terminal` | terminal projektu |
| `/ui/<projekt>/settings` | edycja `board.yaml` i `project.yaml` |
| `/static/...` | CSS, JS, ghostty-web |
| `/sse` | zdarzenia `change-<projekt>` |
| `/term/item/<id>`, `/term/project/<nazwa>` | WebSocket do tmuxa |
| wszystko inne | REST API bez zmian |

Taby feed i graph nie powstają, dopóki nie ma za nimi widoków (Faza 4 i 6).

### Autoryzacja w przeglądarce

`GET /server` po sockecie zwraca też `url` z tokenem. Pierwsze wejście na `/?token=...` ustawia ciasteczko `HttpOnly; SameSite=Strict` i przekierowuje na adres bez tokena. Middleware przyjmuje nagłówek `Bearer` albo ciasteczko. `SameSite=Strict` blokuje żądania z obcych stron, a `coder/websocket` domyślnie sprawdza `Origin`.

### Nowe endpointy API

| CLI | REST |
|---|---|
| `kanban project demo board` | `GET /project/demo/board` (surowy `board.yaml`) |
| `kanban project demo board edit < board.yaml` | `POST /project/demo/board/edit` |
| `kanban project demo edit < project.yaml` | `POST /project/demo/edit` |
| `kanban item 1 runs` | `GET /item/1/runs` |
| `kanban item 1 runs <plik>` | `GET /item/1/runs/<plik>` |

Zapis boarda jest walidowany: poprawny YAML, unikalne nazwy kolumn, żadna nie nazywa się `archive`, każdy krok ma dokładnie jeden typ. Zły board → 400 i nic nie trafia na dysk.

### Watcher i SSE

- fsnotify na `~/kanban/projects`, z rekursją dodawaną ręcznie i skanem nowego katalogu (S3).
- Filtr plików tymczasowych, debounce 50 ms, jedno zdarzenie `change-<projekt>` na paczkę zmian.
- Własnych zapisów serwera nie odfiltrowuję. Każdy zapis, także runnera, jest zmianą, którą UI ma pokazać. Runner zapisuje tylko przy faktycznej zmianie stanu (Faza 2), więc nie ma zalewu zdarzeń.

### Ręczna zmiana kolumny w pliku

Wynik Fazy 1 zostawił lukę: zmiana `column:` w `123.yaml` edytorem nie odpala kroków nowej kolumny, bo `status: done` zostaje ze starej. Stan dostaje pole `ran: <kolumna>`, ustawiane przy każdej zmianie kolumny przez aplikację. Runner widząc `column != ran` traktuje to jako przesunięcie: log `moved` z `message: external`, start kroków od zera.

### Terminal

Kod z S1: pty z `tmux -L kanban attach`, ramki binarne i `resize`, obsługa kółka przez SGR. Sesja projektu nazywa się `project-<nazwa>` i startuje w repo. Sprzątanie runnera dotyka tylko sesji `kanban-<liczba>`.

### Wygląd (punkty 19-20)

- Tło czarne, tekst i obrysy w skali szarości, jedyny "kolor" to odwrócony biały blok dla ACTION REQUIRED.
- Wszystko monospace, prostokąty, `border-radius: 0` globalnie.
- Kolory jako zmienne CSS w jednym miejscu.
- Kolumny przewijane w poziomie, więc board działa też na telefonie.

## Kroki

1. Zależności: fsnotify, creack/pty, coder/websocket, vendorowany ghostty-web.
2. Endpointy API z tabeli, `ran`, sesje projektu w GC.
3. Watcher + SSE.
4. Autoryzacja ciasteczkiem, szablony, CSS, `app.js`.
5. Kanban z drag and drop i edytorem, widok taska, settings, terminal.

## Weryfikacja

Test end-to-end w Go:
- bez tokena 401, `?token=` ustawia ciasteczko i przekierowuje, z ciasteczkiem strona boarda zawiera kolumny i karty
- SSE dostaje `change-demo` po `kanban item new` i po ręcznym zapisie pliku
- ręczna zmiana `column:` w `123.yaml` odpala kroki nowej kolumny
- zły `board.yaml` przez API → 400, plik bez zmian
- WebSocket terminala: wysłane `echo` wraca w strumieniu

Ręcznie w Chrome: każdy widok na desktopie i w wąskim oknie, drag and drop, dodanie taska z edytora, approve i retry z widoku taska, zapis settings, terminal taska i projektu z działającym Claude Code.
