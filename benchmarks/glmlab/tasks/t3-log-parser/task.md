Задача: в текущем каталоге лежит app.log и заготовка parser.py. Реализуй две функции строго по спецификации:

1) parse_log(path) — читает файл построчно и возвращает список dict для КАЖДОЙ валидной строки. Валидная строка имеет вид:
   `YYYY-MM-DDTHH:MM:SSZ LEVEL module=NAME key=value ...`
   - LEVEL — одно из INFO, WARN, ERROR, DEBUG (между LEVEL и module любое число пробелов);
   - после module=NAME идут пары key=value; значение в кавычках берётся как есть (без кавычек), значение без кавычек — до следующего пробела;
   - каждая запись: {"ts": "строка времени как в логе", "level": LEVEL, "module": NAME, "attrs": {все остальные пары}}.
   Невалидные строки (не хватает частей, LEVEL не из списка, невозможное время и т.п.) пропускаются молча.

2) summarize(entries) — принимает результат parse_log и возвращает dict:
   {"total": всего записей, "levels": {LEVEL: количество}, "modules": {MODULE: количество}, "errors_by_code": {значение attrs.code у записей с level=ERROR: количество} (только те, где attrs содержит "code")}.

Скрипт уже умеет писать результат в файл: запускать как
   python parser.py app.log out.json
(вывод — JSON с ensure_ascii=False, indent=2, sort_keys=True — уже сделано в заготовке).

Проверка: python (полный путь интерпретатора: C:\Users\konstantin\AppData\Local\Python\pythoncore-3.14-64\python.exe). Работай только в текущем каталоге. Когда всё готово — ответь одной строкой: DONE
