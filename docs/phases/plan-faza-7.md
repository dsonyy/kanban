# Faza 7 - agent czekający na człowieka i stan w pamięci

Wejście: dwie uwagi użytkownika po weryfikacji końcowej, [weryfikacja-koncowa.md](weryfikacja-koncowa.md).

## 1. Agent, który czeka na input, zanim zacznie sesję

### Problem

Kanban przed startem kroku czyta `~/.claude.json` i `~/.codex/config.toml` i odtwarza logikę zaufania obu harnessów. Ta logika to rekonstrukcja z binarek: zepsuje się przy pierwszej aktualizacji, a i tak nie obejmuje innych rzeczy, na które agent może czekać przy starcie (logowanie, aktualizacja, wybór modelu).

### Decyzja

Sprawdzanie zaufania znika. Kanban obserwuje, co się dzieje po starcie:

- Krok z harnessem dostaje w stanie `session_seen: false`. Hook `session-start` ustawia `true`.
- Jeśli po `idle` (domyślnie 20 s dla harnessów z integracją) nie było `session-start`, agent czeka na coś przed sesją. ACTION REQUIRED z ostatnimi liniami ekranu, żeby było widać pytanie.
- Człowiek odpowiada w terminalu taska w przeglądarce albo przez `kanban item N reply`.
- `session-start` po takim zgłoszeniu zdejmuje ACTION REQUIRED z eventem `resumed`.

Działa dla każdego harnessu i tylko wtedy, gdy dany agent faktycznie startuje, więc nic nie dotyczy Codeksa, dopóki nikt go nie użyje.

## 2. Stan projektu w pamięci

### Co jest faktycznie wyścigiem

Zapisy wewnątrz kanbana idą przez jeden mutex, a testy z serwerem zbudowanym z `-race` nie zgłosiły nic. Realny wyścig jest między kanbanem a zewnętrznym edytorem: kanban czyta plik, człowiek go zapisuje, kanban zapisuje swoją wersję i edycja przepada. Sama kopia w pamięci go nie usuwa, bo ten sam wyścig zostaje między pamięcią a dyskiem. Usuwa go sprawdzenie przed zapisem, czy dysk nadal zawiera to, co pamięć uważa za aktualne.

### Decyzja

Warstwa w pamięci między aplikacją a dyskiem, z trzema elementami:

- **Lustro:** w pamięci surowa treść i sparsowana wartość plików stanu: `project.yaml`, `board.yaml`, `items/N.yaml`, `items/N.md`. Runner, API i web czytają tylko z lustra. Logi eventów, przebiegi, artefakty i sugestie zostają na dysku: są dopisywane albo czytane na żądanie i nie biorą udziału w wyścigu.
- **Reader:** watcher plików przy każdej zmianie wczytuje plik do lustra. Zapis identyczny z lustrem, np. echo własnego zapisu, jest ignorowany. Co 30 s pełne przeskanowanie jako siatka bezpieczeństwa, bo fsnotify potrafi zgubić zdarzenia. Plik pusty albo niepoprawny nie nadpisuje ostatniej dobrej wartości, tylko oznacza wpis jako nieczytelny.
- **Writer:** każda zmiana przechodzi przez jedną funkcję zapisu. Tuż przed zapisem writer porównuje dysk z treścią, na której zmiana była liczona. Jeśli się różnią, wygrywa wersja z dysku: writer wczytuje ją do lustra i zmiany nie zapisuje. API zwraca wtedy 409, a runner ocenia task od nowa w następnym obrocie.

Writer zapisuje synchronicznie w ramach zmiany, bez kolejki w tle. Odpowiedź API oznacza, że zmiana jest na dysku, a padnięcie serwera nie gubi zapisów z kolejki.

Między porównaniem a `rename` zostaje okno rzędu mikrosekund. Zamknięcie go wymagałoby blokady plików, której edytory nie respektują.

### Zmiana w requirements

Punkt 12 ("każda interakcja wczytuje stan z dysku") zmienia się na: dysk jest źródłem prawdy, aplikacja czyta z lustra synchronizowanego z dyskiem, a zmiana zewnętrzna zawsze wygrywa z równoczesną zmianą aplikacji.

## Weryfikacja

- Agent czeka na input przed sesją → ACTION REQUIRED z tekstem pytania → `reply` → `resumed` → krok kończy się normalnie.
- Test na poziomie pakietu: plik zmieniony na dysku między odczytem a zapisem → zmiana aplikacji odrzucona, lustro ma wersję z dysku, na dysku zostaje zmiana zewnętrzna.
- Zmiana pliku z pominięciem watchera jest wykryta przez pełne przeskanowanie.
- Wszystkie dotychczasowe testy end-to-end przechodzą bez zmian w oczekiwaniach.
- Pełny zestaw z serwerem `-race`.
