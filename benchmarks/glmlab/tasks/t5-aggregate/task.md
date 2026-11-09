Задача: в текущем каталоге лежит sales.csv (колонки: date,product,category,qty,unit_price) и заготовка aggregate.py. Реализуй скрипт, который читает sales.csv и пишет summary.json.

Формат summary.json (все суммы = qty*unit_price, округление до 2 знаков после запятой; ключи отсортированы — как даёт json.dump(..., indent=2, sort_keys=True, ensure_ascii=False)):
{
  "total_revenue": сумма по всем строкам,
  "by_category": {"Large": ..., "Small": ...},
  "monthly": {"2026-01": revenue за январь, ...},
  "top_product": имя продукта (product) с наибольшей суммарной выручкой,
  "units_sold": всего продано единиц (сумма qty)
}

Запуск: python aggregate.py sales.csv summary.json (argv: вход, выход).
Проверка: python (полный путь интерпретатора: C:\Users\konstantin\AppData\Local\Python\pythoncore-3.14-64\python.exe). Работай только в текущем каталоге. Когда всё готово — ответь одной строкой: DONE
