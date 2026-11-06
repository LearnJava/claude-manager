# GLM-PLAN — «Claude GLM» как выбираемый мозг в GUI

Детальный план для реализующей сессии. Ветка: `p1-glm-provider` (создана от master,
включает этот план и `AI-STACK.md`). Все номера строк — master @ 62fef46.

Базовые документы: `AI-STACK.md` (модель мира, Часть III — заповеди и контракт),
`docs/runtimes.md` (устройство Runtime). Проверенные факты, на которые опирается
план: CLI поддерживает `--settings <файл>` (приоритет выше пользовательского
settings.json); фасад z.ai мапит `--model glm-5.3` напрямую и `--effort` →
low/high/max (дефолт max); модели `glm-5.3` (текст), `glm-5.3-flash`
(мультимодальная), суффикс `[1m]` + `CLAUDE_CODE_AUTO_COMPACT_WINDOW=1000000`.

---

## 1. Что строим (глазами пользователя)

В GUI при старте сессии появляется выбор ИИ. Помимо «Claude» — новый пункт
**«Claude GLM»**. При его выборе список моделей выглядит так (плоский список
комбинаций модель×effort, ровно как просил владелец):

```
GLM-5.3 low
GLM-5.3 high
GLM-5.3 max
GLM-5.3-Flash low
GLM-5.3-Flash high
GLM-5.3-Flash max
```

Выбор «GLM-5.3 low» запускает claude-рантайм с файлом настроек этой модели
(`--settings`), моделью `glm-5.3` и effort `low`. Выбор запоминается как дефолт
сессии (как сегодня помнится model/effort — `rememberSessionModel`, app.go:537).

Ключ API **не пишется на диск** — он передаётся в окружение дочернего процесса.

## 2. Архитектурное решение

Это **НЕ новый Runtime**. Провайдер — смена мозга внутри существующего
claude-рантайма (AI-STACK, Ось 2). Заповеди Части III соблюдаются: ядро
(`internal/session`) не знает слова «glm» — оно получает абстрактные
«доп. файл настроек» и «доп. env»; конкретика провайдера живёт в конфиге и
app-слое.

Слойка ответственности:

| Слой | Что делает |
|---|---|
| `internal/config` | тип `ProviderConfig`, секция `[providers.glm]` с дефолтами; генерация файлов настроек |
| `app.go` (app-слой) | биндинги: список провайдеров для GUI, старт с провайдером, запоминание выбора |
| `internal/session` | generic: новое поле «settings file» → `--settings` в argv; новый список «provider env» → в `cmd.Env` |
| `frontend` | выбор ИИ + плоский список комбинаций; всё приходит с бэкенда, GLM в GUI не захардкожен |

## 3. Конфиг

`internal/config/types.go` — новые типы:

```go
type ProviderConfig struct {
    DisplayName string          `toml:"display_name"`
    BaseURL     string          `toml:"base_url"`
    KeyEnv      string          `toml:"key_env"`       // имя env-переменной с ключом; сам ключ в конфиге НЕ хранится (прецедент — types.go:21)
    SettingsDir string          `toml:"settings_dir"`  // относительно каталога конфига менеджера
    Models      []ProviderModel `toml:"models"`
}
type ProviderModel struct {
    ID      string   `toml:"id"`      // "glm-5.3" — пойдёт в --model как есть
    Label   string   `toml:"label"`   // "GLM-5.3"
    Efforts []string `toml:"efforts"` // ["low","high","max"]
}
// AppConfig: Providers map[string]ProviderConfig `toml:"providers"`
```

Дефолт в `config.go` (id провайдера — `glm`):

```toml
[providers.glm]
display_name = "Claude GLM"
base_url = "https://api.z.ai/api/anthropic"
key_env = "ZAI_API_KEY"
settings_dir = "state/glm"
[[providers.glm.models]]
  id = "glm-5.3"; label = "GLM-5.3"; efforts = ["low","high","max"]
[[providers.glm.models]]
  id = "glm-5.3-flash"; label = "GLM-5.3-Flash"; efforts = ["low","high","max"]
```

Пользователь может доопределить `[1m]`-варианты в своём config.toml (для них в
файл настроек добавляется `CLAUDE_CODE_AUTO_COMPACT_WINDOW`). Валидация
(`config/validate.go`): id непустые и уникальны, efforts ⊆ известного набора,
key_env непустое имя переменной (не значение!).

## 4. Файлы настроек (генерация, «разные файлы на модель»)

Новый `internal/config/providers.go`: `EnsureProviderSettings(cfg, cfgPath)` —
идемпотентно (пишет только отсутствующее!) создаёт
`<cfgDir>/<settings_dir>/<model-id-sanitized>.json`, например
`~/.claude-manager/state/glm/glm-5.3.json`:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.z.ai/api/anthropic",
    "API_TIMEOUT_MS": "3000000",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "glm-5.3",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "glm-5.3",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "glm-5.3"
  }
}
```

- `ANTHROPIC_DEFAULT_*` = id модели (страховка: даже если что-то резолвит
  алиасы haiku/sonnet/opus само — попадёт в ту же GLM).
- `ANTHROPIC_AUTH_TOKEN` в файл **не пишем** — только в env процесса (раздел 5).
- Для id с суффиксом `[1m]` добавлять `"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "1000000"`.
- Вызвать из `App.startup` после загрузки конфига (app.go:~95) и после SetConfig.
- Разные модели → разные файлы — требование владельца выполнено буквально;
  effort в файлы не дублируем, он идёт живым флагом `--effort`.

## 5. Ядро: generic-поля сессии (без имён провайдеров!)

`internal/session`:

1. `Params` + `SessionConfig`: новое поле `Provider string` (id; "" = обычный
   Claude/Anthropic). Внутри Session хранить резолвнутые артефакты:
   `settingsFile string` и `providerEnv []string` — заполняются в `New`/старте
   из Params (кто резолвит — менеджер, см. раздел 6).
2. `buildCLIArgs` (session.go:2696+): при непустом `settingsFile` —
   `args = append(args, "--settings", settingsFile)`. Модель и effort уже
   идут существующими `--model` / `--effort` (2718) — для glm-модели ничего
   менять не надо, id проходит как есть.
3. Env процесса — точка сборки уже есть, session.go:1659-1666: после
   `utf8Env` добавить `cmd.Env = withEnv(cmd.Env, k, v)` для каждой пары
   `providerEnv`. Менеджер кладёт туда `ANTHROPIC_AUTH_TOKEN =
   os.Getenv(provider.KeyEnv)` (пусто — просто не класть, сессия сама упадёт
   с понятной auth-ошибкой; залогировать warning на старте).
4. Контрактная чистота: в `internal/session` не должно появиться строк
   "glm"/"z.ai". Guard: grep-тест (см. раздел 9).

## 6. Менеджер и app-слой

1. `SessionManager.StartSessionWithOverride` (manager.go:760) — расширить до
   варианта с provider (или новый метод `StartSessionWithProvider(project,
   name, provider, model, effort)`; старый оставить делегатом с provider="").
   Резолв: provider id → `ProviderConfig` → `settingsFile` (проверить, что
   файл существует — если нет, EnsureProviderSettings) + `providerEnv`.
   ВАЖНО: тот же резолв обязан пройти `startSessionResuming` (manager.go:1081,
   живой SetSessionModel) и обычный `StartSession` — provider берётся из
   сохранённого конфига сессии (см. п.3).
2. `SessionConfig` в config.toml сессии получает `provider = "glm"` —
   пишется при первом старте с провайдером и переживает рестарты (так
   ночные сессии стартуют с GLM без человека).
3. `app.go`:
   - `GetProviders() []ProviderInfo` — для GUI: id, displayName, модели
     (id/label/efforts). GUI не хардкодит GLM.
   - `StartSessionWithProviderAndModel(project, name, provider, model, effort)`
     — новый биндинг; старый `StartSessionWithModel` (app.go:528) оставить как
     обёртку (provider="") — совместимость.
   - `rememberSessionModel` (app.go:537) — расширить на provider: хранить
     рядом с model/effort (изучить, где он хранит — config-слой), включая
     запись `provider` в SessionConfig.
   - `SetSessionModel` на лету (app.go:994 / manager.go:1014): если смена
     модели пересекает границу провайдера (claude↔glm) — интерактивный
     SetSessionModel уже делает stop+resume — убедиться, что resumed-старт
     несёт provider нового выбора.
4. Recommendation/autorouting: для не-пустого провайдера автоподбор модели
   пока НЕ поддерживается — ModelPicker в этом случае сразу показывает
   ручной выбор (честный scope-cut, отметить в PR).

## 7. Frontend

Файлы: `ModelPicker.svelte`, `Sidebar.svelte` (pickerFor flow, ~574),
`SessionCard.svelte` (лейбл модели), `lib/models.ts`, i18n `locales/{ru,en}/*`,
тесты `frontend/tests/`.

1. `models.ts`: текущие `MODELS`/`EFFORTS` остаются фолбэком для провайдера
   «Claude». Новое: тип `ProviderUI {id, label, models: {id,label,efforts}[]}`.
   Источник — `GetProviders()` с бэкенда (при ошибке/пусто — только Claude).
2. `ModelPicker.svelte`: сверху выбор ИИ (сегмент-контрол «Claude | Claude GLM»).
   При Claude — как сейчас (два дропдауна model+effort). При Claude GLM —
   ОДИН плоский список комбинаций, ровно в виде «GLM-5.3 low» … «GLM-5.3-Flash
   max» (label модели + пробел + effort). dispatch `confirm` расширяется:
   `{provider, model, effort}`.
3. `Sidebar.svelte`: `on:confirm` (сейчас ~574) — вызвать новый биндинг
   `StartSessionWithProviderAndModel`. Preflight-ветка и ResumePrompt-ветка
   не меняются.
4. Карточка сессии: `normalizeModel` пропускает неизвестный id как есть —
   `glm-5.3` отобразится само; проверить `modelLabel` и места, где статус
   строит строку модели; effort-лейбл рядом, как для claude-моделей.
5. Wails-биндинги регенерируются `wails build` (CLAUDE.md: никогда голый
   `go build`); `tests/helpers/bridge.ts` — добавить моку нового биндинга.
6. Тесты (по образцу `tests/session.spec.ts`, `resume.spec.ts`):
   - пикер: у Claude GLM ровно 6 пунктов, текст «GLM-5.3 low» и т.д.;
   - выбор комбинации → вызван StartSessionWithProviderAndModel с
     (glm, glm-5.3, low);
   - выбор Claude → старый путь без provider;
   - карточка показывает glm-5.3.

## 8. Порядок работы (шаги с проверками)

1. Изучить: `runtime_claude.go`, `session.go:1595-1720` (runOnce/argv/env),
   `manager.go:663-1160`, `app.go:521-560, 866-900`, `ModelPicker.svelte`,
   `Sidebar.svelte:560-620`, `models.ts`, `docs/runtimes.md` §6-7.
2. Конфиг: типы + дефолт glm + валидация + `config_test.go`.
3. `EnsureProviderSettings` + тесты (создание, идемпотентность, [1m]-вариант,
   отсутствие ключа в файлах).
4. Ядро: Provider/SettingsFile/providerEnv; `--settings` в argv; env-инъекция;
   тесты buildCLIArgs (есть образцы в session_test.go / app_config_test.go) —
   новый: со settingsFile аргумент присутствует, без — отсутствует.
5. Менеджер: StartSessionWithProvider + резолв во всех трёх путях старта
   (обычный, override, resuming) + тест.
6. app.go: GetProviders, новый биндинг, rememberSessionModel+provider,
   запись provider в SessionConfig.
7. Frontend: models.ts → ModelPicker → Sidebar → карточка → i18n → тесты.
8. `fakeclaude` (cmd/fakeclaude): научить логировать/принимать `--settings`
   (если ещё не принимает) — для сквозных тестов.
9. Guard-тест «ядро не знает провайдеров»: grep-тест, падающий при появлении
   строк "glm"/"z.ai" в `internal/session/*.go` вне _test (первые шаги к
   guard-тесту из AI-STACK III.2).
10. Сборка `wails build`; `go test ./...` (вывод в лог, только сводка);
    `npm test` в frontend; ручной проход GUI.
11. Коммит по протоколу (cm-task-finish: merge --no-ff в master).

## 9. Критерии приёмки

- [ ] В пикере есть «Claude GLM» и ровно 6 комбинаций с точными подписями.
- [ ] Старт «GLM-5.3 low»: в argv процесса есть `--settings <…>/glm-5.3.json`,
      `--model glm-5.3`, `--effort low`; в env процесса — ANTHROPIC_AUTH_TOKEN
      из переменной `ZAI_API_KEY`.
- [ ] Файлы настроек сгенерированы автоматически, ключа в них нет; повторный
      старт не перезаписывает ручные правки файла (идемпотентность).
- [ ] Обычные claude-сессии (provider="") бинарно не изменили поведение
      (регресс: argv без --settings, env без токена) — тесты старые зелёные.
- [ ] Выбор запомнен: рестарт приложения → сессия стартует с GLM без человека.
- [ ] Живая смена модели внутри glm (Sonnet-подобная логика SetSessionModel)
      сохраняет разговор; смена провайдера на лету не роняет сессию.
- [ ] grep-тест: в internal/session нет "glm"/"z.ai".
- [ ] `go test ./...` и фронтовые тесты зелёные.

## 10. Риски и заметки

- **effort через фасад**: доки z.ai документируют маппинг, но наш замер начала
  ноября был отрицательным. Первое, что проверить руками после сборки:
  `claude --settings <файл>` → `/status`, `/effort`. Если не работает —
  оставить флаг (совместимость вперед), в GUI пометить в тултипе.
- `[1m]`-варианты требуют свежий CLI («модель не существует» → обновить).
- Не трогать параллельную работу: ветка `slim-for-kimi` занята другой
  сессией; в её рабочем дереве лежат untracked `AI-STACK.md` и `GLM-PLAN.md` —
  при синхронизации с master удалить локальные копии ДО merge, иначе untracked
  конфликт.
- Пуш не делать без указания владельца.
- Скоуп-кат: recommendation (автоподбор модели) для GLM не делаем; `[1m]` в
  дефолт не включаем (пользователь добавит сам в config.toml при желании).
