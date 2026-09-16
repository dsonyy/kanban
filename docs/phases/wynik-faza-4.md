# Faza 4 - wynik

Zaimplementowane zgodnie z [plan-faza-4.md](plan-faza-4.md). Nowe: `feed.go`, `web/templates/feed.html`, `feed_test.go`. Zmiany w runnerze, store, gramatyce i widokach.

## Stan

- `kanban feed` ≡ `GET /feed`: otwarte pozycje ze wszystkich projektów od najdłużej czekającej, historia ostatnich 100 zdarzeń `attention` i `failed` z oznaczeniem otwarte/zamknięte, suma czekania z 24 h.
- Tab `feed` z akcjami approve, retry, odpowiedź do działającego kroku, otwórz. Odświeża się na każdą zmianę w dowolnym projekcie. Odświeżenie czeka, jeśli właśnie piszesz odpowiedź.
- `kanban item N reply` ≡ `POST /item/N/reply`: tekst i Enter do panelu działającego kroku, event `replied`.
- Push przez ntfy przy każdym nowym `attention` i `failed`, z linkiem do taska. Włączany przez `ntfy` w `config.yaml`, domyślnie wyłączony.
- Czas czekania na człowieka w widoku taska i w feedzie, liczony z logu eventów.
- Nowy event `resumed`, gdy ACTION REQUIRED znika bez akcji człowieka (agent wznowił output, board naprawiony).

## Weryfikacja

`go test ./...`, 13 testów end-to-end, ~60 s. Nowe:

| Test | Sprawdza |
|---|---|
| `TestFeedAndPush` | dwa projekty, krok `human` i padający krok w feedzie w dobrej kolejności, dwa POST-y do fałszywego serwera ntfy z tytułem, treścią i linkiem pod skonfigurowany `url`, po approve pozycja znika z open i zostaje w historii, czas czekania > 0 w feedzie i w widoku taska |
| `TestNoPushWithoutConfig` | bez konfiguracji zero żądań |
| `TestReplyAndResumed` | krok czytający stdin dostaje odpowiedź przez `reply`, przebieg zawiera ją w outpucie, log ma `replied` i `resumed`, `reply` do nieistniejącego taska kończy się błędem |

Celowe psucie kodu, złapane: brak eventu `resumed`, wyłączona wysyłka push.

Ręcznie w Chrome na demo z dwoma projektami: feed z siedmioma pozycjami, odpowiedź `eu-west-1` z feedu do agenta pytającego o region, pozycja zniknęła z feedu bez przeładowania, log taska: `replied → resumed → finished → done`.

## Decyzje podjęte w trakcie

- **ntfy zamiast Web Push.** Uzasadnienie w planie.
- **W feedzie komunikat jako plakietka, nie pełny biały blok.** Przy siedmiu pozycjach pełne bloki zlewały się w ścianę bieli i nic się nie wyróżniało. Na boardzie i w widoku taska blok zostaje, bo tam jest jeden.

## Znane ograniczenia, świadomie zostawione

- **Push nie był testowany z prawdziwym ntfy.sh.** Test używa fałszywego serwera. Wysyłanie treści tasków do zewnętrznej usługi z mojej inicjatywy byłoby poza zakresem. Do sprawdzenia po ustawieniu własnego tematu w `config.yaml`.
- **Nie widziałem powiadomienia na telefonie.** Jak wyżej.
- **Komunikat `no output for 6s` zamraża czas z chwili wykrycia.** Aktualny czas oczekiwania jest obok w `waiting`.
- **Feed skanuje wszystkie logi wszystkich tasków przy każdym odświeżeniu.** Przy setkach tasków to milisekundy. Przy dziesiątkach tysięcy trzeba będzie indeksu.
- **Zrzuty ekranu w Chrome przestawały działać, gdy okno przeglądarki było zasłonięte.** Sprawdzone przez `document.visibilityState`. Nie dotyczy aplikacji.

## Dla Fazy 5

- `reply` działa na panelu kroku. Dla Claude Code z integracją będzie można zamiast tego odpowiadać przez wznowienie sesji.
- Metryka czasu czekania zależy od eventów. Hooki z Fazy 5 powinny emitować `attention` przy `Notification` i `resumed` przy `UserPromptSubmit`, wtedy liczy się bez heurystyki `idle`.
