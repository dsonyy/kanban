# Faza 4 - uwaga człowieka: feed i push

Wejście: requirements 17, 22, [wynik-faza-3.md](wynik-faza-3.md).
Wyjście: nie trzeba pilnować boarda. Wszystko, co czeka na człowieka, jest w jednym miejscu, przychodzi na telefon i da się to załatwić bez szukania taska.

## Decyzje

### Feed jako zasób API

`kanban feed` ≡ `GET /feed`. Dwie listy ze wszystkich projektów:
- **open** - taski, które teraz mają `attention`, najdłużej czekające na górze, z czasem oczekiwania
- **recent** - ostatnie 100 eventów `attention` i `failed`, od najnowszych, z informacją, czy nadal otwarte

Ten sam zasób czytają widok w przeglądarce i agenci z CLI. Tab `feed` w pasku widoków pokazuje dane ze wszystkich projektów niezależnie od wybranego projektu.

### Akcje z feedu

- **Otwórz** - link do widoku taska.
- **Approve** dla `waiting`, **Retry** dla `failed` - istniejące endpointy.
- **Odpowiedz** - nowy `kanban item 12 reply` ≡ `POST /item/12/reply`, body to tekst. Trafia jako wpisane klawisze plus Enter do panelu działającego kroku. Bez działającego kroku → 400. Służy agentowi, który zatrzymał się z pytaniem i dostał `idle`.

### Push przez ntfy, nie Web Push

Punkt 22 dopuszcza oba. Web Push wymaga HTTPS, service workera, kluczy VAPID i szyfrowania treści (RFC 8291), czyli kilkuset linii kryptografii albo kolejnej zależności. ntfy to jeden POST, działa bez HTTPS po stronie kanbana, a aplikacja ntfy na telefonie już obsługuje powiadomienia i kliknięcie.

Konfiguracja w `~/kanban/config.yaml`:

```yaml
ntfy: https://ntfy.sh/<prywatny-temat>
url: https://maszyna.tailnet.ts.net
```

- Bez `ntfy` nic nie wychodzi poza maszynę.
- Powiadomienie przy każdym nowym evencie `attention` i `failed`: tytuł `#12 projekt: pierwsza linia`, treść to komunikat, kliknięcie otwiera task pod `url` (albo lokalnym adresem, gdy `url` brak).
- Wysyłka w tle z timeoutem 10 s. Błąd trafia do logu serwera i nie blokuje runnera.

### Metryka czasu oczekiwania (punkt 17)

Liczona z logu eventów, bez nowego stanu. Oczekiwanie zaczyna event `attention` albo `failed`, kończy pierwszy z: `approved`, `retried`, `moved`, `resumed`, `finished`, `done`.

Dziś runner czyści `attention` po wznowieniu outputu albo naprawie boarda bez eventu, więc takie oczekiwanie nigdy by się nie zamknęło. Dochodzi event `resumed`.

Pokazywane: w widoku taska łączny czas czekania na człowieka, w feedzie czas oczekiwania każdego otwartego taska i suma z ostatnich 24 h.

### Odświeżanie feedu

Hub SSE wysyła dodatkowo ogólne `change` przy każdej zmianie. Feed słucha jego, board i task dalej swojego projektu.

## Weryfikacja

Test end-to-end:
- dwa projekty, krok `human` w jednym, padający krok w drugim → `GET /feed` ma oba w open, najstarszy pierwszy
- `approve` → zniknięcie z open, event nadal w recent jako zamknięty, czas oczekiwania > 0 w widoku taska
- fałszywy serwer ntfy w teście: POST z tytułem, treścią i nagłówkiem `Click` po `attention` i po `failed`, zero POST-ów bez konfiguracji
- `reply` do kroku czytającego stdin → krok kończy się, a przebieg zawiera odpowiedź
- `idle` → `resumed` po wznowieniu outputu

Ręcznie w Chrome: feed z kilkoma otwartymi pozycjami z różnych projektów, approve i reply z feedu, odświeżenie na żywo.
