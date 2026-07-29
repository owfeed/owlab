# owlab

[English](README.md)

Одноразовые роутеры OpenWrt и ImmortalWrt. Один файл, одна команда, работающие
роутеры — на Linux, WSL2, Docker Desktop для Windows или macOS.

Чтобы разрабатывать пакет LuCI сразу на нескольких релизах, чтобы проверить
конфиг до того, как он попадёт на роутер, от которого зависит дом, чтобы снять
скриншоты, чтобы понять, что делает настройка, — и чтобы в CI отвечать на
вопрос «мой пакет всё ещё ставится на 24.10?», а это [один
шаг](#проверять-в-ci).

```console
$ go install owfeed.org/owlab/cmd/owlab@latest
$ owlab doctor
```

Без тулчейна Go — взять бинарь из
[последнего релиза](https://github.com/owfeed/owlab/releases/latest): linux,
darwin и windows, amd64 и arm64, — и положить в `PATH`. На Windows это
PowerShell и `owlab.exe`, больше ничего не нужно.

Нужен Docker (или OrbStack, Colima, Podman, Rancher Desktop) с Compose v2. Для
`fidelity: vm` понадобится ещё QEMU: `brew install qemu`,
`apt install qemu-system-arm qemu-system-x86` или
`winget install SoftwareFreedomConservancy.QEMU`.

Что каждой платформе нужно сверх этого — правило про файловую систему WSL2, два
необязательных компонента Windows, группа `kvm` на Linux — собрано в одном
разделе [ранбука](docs/runbook_ru.md#поставить-owlab-на-эту-машину).
`owlab doctor` проверяет всё это и говорит, чем грозит каждое отсутствующее.

Не только разрабатываете пакет, но и выпускаете? [Кукбук](https://owfeed.org/cookbook/ru/)
проходит весь путь — собрать, проверить, подписать, опубликовать — с готовыми файлами.


## Начало

Положите `owlab.yaml` рядом со своим пакетом:

```yaml
version: 1

routers:
  - id: owrt2512
    release: "25.12.5"
```

```console
$ owlab up
  owrt2512   http://localhost:8080   ssh -p 2222 root@localhost
```

Откройте адрес. Логин `root`, пароль оставьте пустым.

## Рецепты

### Проверять на нескольких релизах сразу

```yaml
routers:
  - id: owrt2512
    release: "25.12.5"
  - id: owrt2410
    release: "24.10.8"
  - id: imm2512
    distro: immortalwrt
    release: "25.12.1"
```

Порты назначаются автоматически. Задать свои: `ports: { http: 8025, ssh: 2225 }`.

### Добавить пакеты

На роутере уже есть то, что стоит на серийном устройстве с OpenWrt. Добавляйте
поверх:

```yaml
defaults:
  packages: ["+luci-app-sqm", "+luci-app-ddns"]
```

Убрать через `-`:

```yaml
packages: ["-ppp", "-ppp-mod-pppoe", "+luci-app-sqm"]
```

Список без `+` и `-` заменяет набор по умолчанию целиком.

Поставить пакет без пересборки:

```console
$ owlab install owrt2512 luci-app-ttyd
```

Он исчезнет после `owlab up --rebuild`. `packages:` — нет.

### Поставить пакет, которого нет ни в одном фиде

Свой собственный или что угодно с GitHub Releases. Оба URL, потому что apk и
opkg называют файлы по-разному:

```yaml
defaults:
  extra_packages:
    - name: luci-app-example
      apk: https://github.com/someone/example/releases/download/1.4.0/luci-app-example-1.4.0-r1.apk
      ipk: https://github.com/someone/example/releases/download/1.4.0/luci-app-example-v1.4.0-r1-all.ipk
```

Пиньте точный релиз. Подписи здесь не проверяются.

### Работать над пакетом на живом роутере

```console
$ owlab sync
  owrt2512   42 files, 846 KB
```

Перезагрузите страницу — правки на месте. Чтобы это происходило на каждое
сохранение:

```console
$ owlab sync --watch
```

owlab копирует те же каталоги, что ставит `luci.mk` — `htdocs`, `ucode`,
`luasrc`, `root`. Если пакет лежит в подкаталоге, скажите об этом:

```yaml
project:
  install:
    my-package/htdocs: /www
    my-package/ucode:  /usr/share/ucode/luci
    my-package/root:   /
```

### Собирать ассеты перед каждым sync

Если пакет генерирует файлы, которых нет в дереве — `cascade.css` темы,
скомпилированные переводы, JS-бандл:

```yaml
project:
  build: ./build-css.sh htdocs/luci-static/footstrap/cascade.css
```

### Выполнить что-то на роутере после каждого sync

Регистрация пакета, сброс кеша, перезапуск сервиса:

```yaml
project:
  post_sync: |
    uci -q set luci.themes.Footstrap=/luci-static/footstrap
    uci -q commit luci
```

### Работать над темой

```yaml
project:
  theme: footstrap
```

owlab выбирает её после каждого sync. Без этого тема зарегистрирована, но не
показана — установка пакета темы добавляет `luci.themes.<Name>` и намеренно не
трогает `luci.main.mediaurlbase`.

[examples/luci-theme-footstrap](examples/luci-theme-footstrap/owlab.yaml) —
настоящий пример: четыре роутера, шаг сборки для генерируемого CSS и сторонние
приложения, на которых проверяется каскад.

### Грузить модули ядра или получить настоящий WiFi

```yaml
routers:
  - id: real
    fidelity: vm
    release: "25.12.5"
    packages: ["+kmod-nft-tproxy"]
```

```console
$ owlab up
$ owlab exec real -- 'uname -r; lsmod | grep nft_tproxy'
6.12.94
nft_tproxy    12288  0
```

Это QEMU с собственным ядром OpenWrt, поэтому пакеты `kmod-*` действительно
грузятся. Ещё он получает два радио `mac80211_hwsim`, и они работают —
запускается hostapd, iwinfo отдаёт сигнал и ширину канала, страницы Wireless
показывают роутер, а не его конфиг.

Первый старт около полутора минут, дальше двадцать секунд. Диск хранит всё
установленное и переживает `owlab down`.

Настройки: `memory: 512M`, `cpus: 2`, `disk: 2G`, `radios: 2`. `radios: 0` —
без беспроводного.

### Сделать коробку обжитой

Пустой роутер почти ничего не рисует — ни бейджей зон, ни VLAN, ни аренд DHCP,
ни таблиц со строками. Фикстуры всё это выдумывают:

```yaml
defaults:
  fixtures: [all]
```

Отдельные профили: `networks`, `clients`, `wireguard`, `portforwards`,
`system`, `wifi`. `lived-in` — всё, кроме wifi, и он по умолчанию. `none`
выключает.

### Собрать настоящий .apk или .ipk

```console
$ owlab build
$ owlab install owrt2512 dist/noarch/luci-app-mine-1.0-r1.apk
```

Через SDK от OpenWrt. Стоит делать перед релизом: настоящая сборка минифицирует
JS и CSS, а sync — нет, и на этой разнице ломались пакеты, прекрасно работавшие
на дев-боксе.

Артефакты кладутся в каталог, названный по архитектуре: в имени apk-файла
архитектуры нет вообще, и всё, что идёт дальше, читает каталог. Архитектурно
независимый пакет даёт оба написания — apk требует `noarch` и отвергает `all`,
opkg знает только `all`, — поэтому одна сборка пишет два каталога:

```
dist/
├── noarch/luci-app-mine-1.0-r1.apk
└── all/luci-app-mine_1.0-r1_all.ipk
```

Эта раскладка — [контракт артефакта owfeed][artifact-contract]; именно она
позволяет отдать вывод `owlab build` инструменту публикации напрямую, без того
чтобы они знали друг о друге. `--layout flat` возвращает прежний плоский вывод
на один релиз.

[artifact-contract]: https://github.com/owfeed/owfeed/blob/main/docs/artifact-contract.md

### Проверять в CI

```yaml
- uses: owfeed/owlab/action@v0.5.2
  with:
    releases: "25.12.5 24.10.8"
    install: dist/*/luci-app-mine-*.apk
    assert: |
      http 200 /cgi-bin/luci/admin/services/mine
      service mined
      uci mine.@mine[0].enabled
```

`owlab.yaml` не нужен — конфигурация это и есть номера релизов. Джоб падает,
если пакет не поставился, страница не отрисовалась или сервис не поднялся, а в
summary написано, какой роутер и какая проверка.

То же самое локально — и ровно это запускает экшен:

```console
$ owlab test --release 25.12.5 --release 24.10.8 \
    --install 'dist/*/luci-app-mine-*.apk' \
    --assert 'http 200 /cgi-bin/luci/admin/services/mine'
```

Поднять, поставить, проверить, снести, вернуть 0 или 1. `--keep` оставляет
роутеры поднятыми, `--json` пишет отчёт в stdout, а лог упавшего роутера
печатается, пока контейнер ещё существует.

Две вещи, которых не делает написанный руками `curl`. Он **логинится в LuCI под
root** — каждая страница, ради которой существует приложение, лежит под
`/cgi-bin/luci/admin`, и неавторизованный запрос туда это редирект на форму
логина; поэтому очевидная проверка отдаёт 403 на совершенно здоровой странице. И
он **валит 200, чьё тело — страница ошибки**: поймав исключение, диспетчер LuCI
всё равно отвечает 200, положив трейс в тело.

Полный список ассершенов и формат JSON:
[docs/reference_ru.md](docs/reference_ru.md#owlab-test).


### Проверить, что пакет ставится из фида, а не только из файла

```console
$ owlab test --release 25.12.5 \
    --feed 'https://example.org/releases/25.12/x86_64/packages.adb' \
    --feed-key ./myfeed.pem \
    --install my-app \
    --assert 'http 200 /cgi-bin/luci/admin/services/mine'
```

Установка файла доказывает, что работает пакет. Установка **по имени** из
подписанного индекса доказывает, что работает канал: индекс читается, URL не
редиректит, ключ на роутере совпадает с тем, которым подписано. Установка файла
не может провалиться ни по одной из этих причин — значит и обнаружить их не может.

`--allow-untrusted` здесь не используется: пакет принимается именно из-за подписи
индекса. Для opkg *имя* файла ключа обязано быть его id — opkg ищет ключ по имени.
### Попасть на роутер

```console
$ owlab shell owrt2512
$ owlab exec owrt2512 -- 'logread | tail -20'
$ owlab logs owrt2512 -f
$ owlab open owrt2512
```

`ssh -p 2222 root@localhost` тоже работает. owlab ставит все найденные
`~/.ssh/id_*.pub`. Указать другой: `OWLAB_PUBKEY=/path/to/key.pub`.

### Задать пароль root

```console
$ OWLAB_ROOT_PASSWORD=hunter2 owlab up --rebuild
```

По умолчанию пароля нет. Для коробки на localhost это нормально, и LuCI
принимает пустое поле.

### Держать конфиг в другом месте

```console
$ owlab --config ../my-package/owlab.yaml up
$ owlab --config ../my-package status        # каталог тоже подойдёт
$ export OWLAB_CONFIG=~/src/my-package
```

Без этого owlab ищет в текущем каталоге и выше.

В [examples/](examples/) лежат конфиги, на которые можно указать напрямую.

### Узнать, не устарели ли пины

```console
$ owlab releases
ROUTER          DISTRO    PINNED     NEWEST
owrt2512        openwrt   25.12.4    25.12.5    1 release behind
owrt2410        openwrt   24.10.8    24.10.8    up to date
```

`owlab releases --all` покажет всё, что публикуют серверы загрузок.

Пиньте точные точечные релизы и двигайте их осознанно. Rootfs и его фид обязаны
называть один релиз; смешение роняет любую установку с `breaks: world[...]`.

### Начать с нуля

```console
$ owlab up --rebuild        # выбросить роутеры и собрать заново
$ owlab down --purge        # и снести образы с дисками VM
```

## Команды

```
owlab up          собрать и запустить роутеры
owlab down        остановить роутеры
owlab sync        скопировать пакет и перезагрузить LuCI
owlab shell       открыть шелл
owlab exec        выполнить команду
owlab install     поставить пакеты на работающий роутер
owlab build       собрать настоящий .apk/.ipk через SDK
owlab logs        лог загрузки и сервисов
owlab releases    сверить пины с серверами загрузок
owlab status      что запущено и где
owlab open        открыть LuCI в браузере
owlab doctor      проверить эту машину
owlab version     показать версию owlab
```

Любая команда принимает id роутеров. Без них действует на все.
`--config <path>` (или `-c`) работает с любой из них, до и после имени команды.
У `test`, `status` и `releases` есть `--json`.

## Когда что-то сломалось

Сначала `owlab doctor`. Он проверяет порты, ssh-ключи, окончания строк, QEMU и
то, пропускает ли ваш движок DNS.

Дальше [docs/troubleshooting_ru.md](docs/troubleshooting_ru.md). Почти всё, что
здесь ломается, выглядит как что-то другое: роутер, который отдаёт LuCI, но
сбрасывает каждое соединение; пакет, который поставился и ничего не делает;
радио, которые существуют, но не поднимаются. Всё это там, отсортировано по
симптомам.

В [docs/runbook_ru.md](docs/runbook_ru.md) — процедуры: задача, команды и как
убедиться, что вышло.

## Рядом с чем это стоит

[containerlab](https://containerlab.dev/) строит топологии — много узлов, линки
между ними — и поддерживает OpenWrt как вид узла. owlab строит один роутер, под
который вы пишете: топологии нет, зато есть `sync` с горячей перезагрузкой,
fixtures, сборка через SDK и `test`. Два роутера, гоняющих BGP друг в друга, —
это containerlab; одна и та же страница LuCI, открытая на 24.10 и 25.12, пока вы
её правите, — это сюда.

[owfeed](https://github.com/owfeed/owfeed) публикует пакеты: собирает,
подписывает и индексирует apk-фид, а `owfeed smoke` ставит результат на живой
образ OpenWrt перед публикацией. Последний шаг тоже поднимает контейнер, и
сходство намеренное: owlab — это цикл разработки, `owfeed smoke` — одна проверка
перед публикацией. Они независимы специально, чтобы ответ на «поставится ли
фид» никогда не зависел от того, правильно ли установлен owlab.

## Заметки

Логин `root` с пустым паролем, если вы не задали свой.

Хост-ключи ssh вшиты в образ и общие у всех, кто им пользуется. Не выставляйте
эти роутеры в сеть, которая вам дорога́.

Пиньте точные точечные релизы. Образы и фиды `snapshot` пересобираются ежедневно
и независимо, так что установки начинают падать в течение суток.

## Документация

В [docs/](docs/) остальное, на английском и русском: ранбук, журнал диагностики,
справочник по конфигу, устройство и порядок выпуска релизов.
[CONTRIBUTING.md](CONTRIBUTING.md) — что стоит знать, прежде чем что-то менять;
[SECURITY.md](SECURITY.md) — чем эти роутеры являются и чем нет.

## Лицензия

GPL-2.0-only, и это осознанный выбор — [owfeed](https://github.com/owfeed/owfeed)
того же автора под Apache-2.0. owlab встраивает оверлей из `/etc/uci-defaults` и
`rc.local`, написанный поверх шелл-библиотек самого OpenWrt, и кладёт его внутрь
каждого собираемого образа; это производная работа. Вызов owlab из вашего CI,
Makefile или своего инструмента не накладывает на ваш код ничего — обязательства
возникают при распространении изменённого owlab.

OpenWrt — зарегистрированный товарный знак Software Freedom Conservancy. owlab
не аффилирован с проектом OpenWrt или SFC и ими не одобрен.
