# Dziennik prób zepsucia

Każda iteracja: jeden nowy kąt ataku, wynik, a przy błędzie test end-to-end, który failował przed poprawką.

| # | Data | Kąt ataku | Wynik |
|---|---|---|---|
| 1 | 2026-09-16 | 300 równoległych zmian przez CLI (move, edit, link, unlink, odczyt) na 5 taskach, podczas gdy runner przesuwa je przez kolumny z krokami i `goto` | Bez błędu: zero błędów API, serwer żywy, każdy `N.yaml` poprawny, lustro zgodne z dyskiem |
| 2 | 2026-09-16 | Nazwy kolumn z `/`, `next`, spacjami na brzegach | **Błąd.** Kolumna `a/b` była akceptowana, a przesunięcie do niej kończyło się `unsupported`, bo slash rozbija ścieżkę API. `next` kolidowało z `goto: next`. Walidacja w `saveBoard` odrzuca teraz takie nazwy. Test: `TestBoardRejectsUnreachableColumnNames`. Tę część zaczął osierocony przebieg tego samego zadania, niewidoczny w kontekście sesji, a przejęty i sprawdzony w obie strony w tej iteracji |
| 3 | 2026-09-16 | Nazwy kolumn ze znakami specjalnymi URL: `%`, `?`, `#`, `+&=`, spacja, polskie znaki, HTML | **Błąd.** CLI sklejało URL z surowych segmentów: `50%` dawało `invalid URL escape`, a `q?x#y` było ucinane do `q`. Segmenty są teraz kodowane przez `url.PathEscape`. HTML w nazwie kolumny był już bezpiecznie escapowany w UI. Test: `TestColumnNamesThatNeedEscaping` |
