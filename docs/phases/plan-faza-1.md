# Faza 1 - rdzeń: dysk, serwer, CLI

Wejście: requirements 1-17, 34-38, [wynik-faza-0.md](wynik-faza-0.md).
Wyjście: `kanban item new`, `kanban item 123`, `kanban item 123 move todo` działają przez serwer, a ręczna edycja pliku jest widoczna w następnym odczycie.

## Decyzje

### Jedna gramatyka dla CLI i REST

Ścieżka to pary `zasób [id]`, potem opcjonalny czasownik z argumentami:

```
[project <nazwa>] [item <id>] [<czasownik> <argumenty>...]
```

- Bez czasownika: `GET`. Z czasownikiem: `POST`. Nie ma PUT ani DELETE, bo nic nie jest kasowane (archiwum, punkt 38), a jedna reguła zamiast czterech mapuje się 1:1 bez tabeli.
- CLI przyjmuje pary zasobów w dowolnej kolejności i układa je w kanoniczną ścieżkę. `kanban item 123 project foo` i `kanban project foo item 123` dają `GET /project/foo/item/123`.
- Ta sama funkcja parsuje argv w CLI i ścieżkę w serwerze. To jest "wspólna definicja zasobów" z planu.
- Treść (content taska) idzie w body: CLI czyta stdin, gdy nie jest terminalem.

| CLI | REST |
|---|---|
| `kanban project` | `GET /project` |
| `kanban project new foo` (w katalogu repo) | `POST /project/new/foo` |
| `kanban project foo` | `GET /project/foo` (board z taskami) |
| `echo treść \| kanban item new [kolumna]` | `POST /item/new[/kolumna]` |
| `kanban item 123` | `GET /item/123` |
| `echo treść \| kanban item 123 edit` | `POST /item/123/edit` |
| `kanban item 123 move todo` | `POST /item/123/move/todo` |
| `kanban item 123 archive` | `POST /item/123/archive` |
| `kanban item 123 log` | `GET /item/123/log` |

Czasowniki są zastrzeżone i nie mogą być nazwą projektu. `log` to wyjątek od reguły "czasownik = POST": to podzasób tylko do odczytu, więc idzie GET-em.

### ID tasków globalne, nie per projekt

`KANBAN_TASK=123` i `KANBAN_PORT` wyliczany z ID (punkt 28) muszą być jednoznaczne na całej maszynie, a hook zna tylko ID. Przy ID per projekt dwa taski z dwóch projektów dostałyby ten sam port. Nowe ID = największe istniejące + 1, liczone skanem pod blokadą. Tasków się nie kasuje, więc ID nie wracają.

Skutek: `kanban item 123` działa bez podawania projektu.

### Układ na dysku

```
~/kanban/                  (KANBAN_HOME, domyślnie ~/kanban)
  kanban.sock              socket, 0600
  kanban.lock              flock pojedynczej instancji
  token                    token do TCP, 0600
  state.yaml               project: <ostatni projekt>
  projects/<nazwa>/
    project.yaml           repo: /ścieżka
    board.yaml             columns: [{name: backlog, steps: []}, ...]
    items/
      123.md               content
      123.yaml             column, created
      123.log.yaml         append-only lista eventów
```

- `steps` w `board.yaml` zapisane, ale w Fazie 1 nieczytane. Składnia kroków to decyzja Fazy 2.
- Nowy projekt dostaje kolumny `backlog, todo, doing, done`. Domyślny workflow z punktu 32 to Faza 6.
- Archiwum to zastrzeżona kolumna `archive`, niewidoczna w `board.yaml` i w widoku boarda. Jedno pole zamiast osobnego katalogu i drugiej ścieżki wyszukiwania.
- Log eventów jako lista YAML: każdy event dopisywany jest jako nowy element `- at: ... event: ...`, plik pozostaje poprawnym YAML-em bez przepisywania.
- `project new` bierze repo z katalogu, w którym odpalono CLI (CLI wysyła nagłówek `Kanban-Cwd`).

### Pojedyncza instancja

- `kanban` bez argumentów: `flock` na `kanban.lock`. Nie udało się → serwer działa, wypisz PID (z `GET /server`) i wyjdź. Udało się → usuń stary socket, nasłuchuj.
- Serwer działa na pierwszym planie. Uruchamianie w tle to kwestia `tmux`/`systemd --user`, nie aplikacji.
- `kanban <argumenty>` bez działającego serwera: błąd i exit 1. Nie startuje serwera automatycznie, bo hook odpalony w złym momencie postawiłby serwer jako dziecko agenta.

### Transport i autoryzacja

- Socket bez tokena, chroniony uprawnieniami pliku.
- TCP na `127.0.0.1:7420` (`KANBAN_ADDR`) z nagłówkiem `Authorization: Bearer <token>`. Token generowany przy pierwszym starcie. W Fazie 1 TCP nie ma jeszcze klienta, ale middleware powstaje od pierwszego endpointu, zgodnie z planem.

### Odpowiedzi

- Zawsze YAML. Błąd: kod HTTP ≥ 400 i body `error: <opis>`.
- CLI wypisuje body jak jest i kończy z exit 1 przy kodzie ≥ 400.

### Zapis

- Serwer jest jedynym writerem przez API. Globalny mutex na zapisy.
- Atomic write: plik tymczasowy z kropką w tym samym katalogu + `rename`.
- Każdy odczyt idzie z dysku, stanu w pamięci nie ma (punkt 12).

## Przesunięcie względem plan.md

**Watcher przechodzi do Fazy 3.** W Fazie 1 każdy request czyta dysk, więc ręczna edycja jest widoczna w następnym odczycie bez watchera. Watcher jest potrzebny dopiero do wypychania zmian do przeglądarki przez SSE, a SSE to Faza 3. Wnioski z S3 czekają w wyniku Fazy 0. Zależność fsnotify dochodzi razem z nim.

## Kroki

1. Moduł Go w katalogu głównym repo, jeden pakiet `main`.
2. Parser gramatyki + kanoniczna ścieżka.
3. Warstwa dysku: projekty, taski, log, atomic write.
4. Serwer: flock, socket, TCP z tokenem, routing przez parser.
5. CLI: parser argv → request po sockecie → stdout, exit code.
6. Test end-to-end.

## Weryfikacja

Test `e2e_test.go` buduje binarkę, stawia serwer w tymczasowym `KANBAN_HOME` i przez prawdziwe CLI sprawdza:
- drugie `kanban` bez argumentów zgłasza PID i kończy się, nie stawiając drugiego serwera
- `project new`, `item new` ze stdin, `item 123`, `move`, `archive`, `log`
- dowolna kolejność par zasobów daje ten sam wynik
- ręczna zmiana `123.md` jest widoczna w następnym `kanban item 123`
- TCP bez tokena → 401, z tokenem → 200
- CLI bez serwera → exit 1

Poza tym ręczne przejście scenariusza z "Wyjście" na prawdziwym `~/kanban`.
