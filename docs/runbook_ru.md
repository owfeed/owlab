# Ранбук

[English](runbook.md)

Процедуры. Каждая — задача, команды и способ убедиться, что вышло.

Что означает ключ — в [справочнике](reference_ru.md). Почему оно так себя ведёт
— во [внутренностях](internals_ru.md). Когда что-то сломалось — в
[диагностике](troubleshooting_ru.md), она отсортирована по симптомам.

---

## Поставить owlab на эту машину

Один бинарь на Go и один контейнерный движок. По платформам различается только
откуда берётся движок и, для `fidelity: vm`, откуда берётся QEMU.

В любом случае проверка — `owlab doctor`: он называет, что есть, чего нет и чем
именно грозит каждое отсутствующее.

### Linux

```console
$ go install owfeed.org/owlab/cmd/owlab@latest
$ owlab doctor
```

Без тулчейна Go — взять `owlab_<версия>_linux_<арх>.tar.gz` из
[последнего релиза](https://github.com/owfeed/owlab/releases/latest) и положить
бинарь в `PATH`.

Docker, Podman или что угодно ещё с Compose v2. Для `fidelity: vm`:

```console
$ sudo apt install qemu-system-x86 qemu-system-arm     # или то же самое через dnf
$ sudo usermod -aG kvm "$USER"                          # и перелогиниться
```

Про группу `kvm` забывают чаще всего: QEMU, собранный с KVM, всё равно
скатывается в трансляцию, пока пользователь не в группе, — doctor говорит, что
из двух получилось.

### WSL2

Ставить и запускать owlab **внутри** дистрибутива WSL, а не из PowerShell по
пути `\\wsl$`. Команды те же, что в Linux. Годится и Docker, поставленный в WSL,
и Docker Desktop с включённой WSL-интеграцией.

Проект держать в файловой системе WSL — `~/src/luci-app-mine`, а не
`/mnt/c/Users/...`. Диск Windows виден через слой трансляции, который не
пробрасывает inotify: `owlab sync --watch` не сработает ни разу, а каждое чтение
файла достаточно медленное, чтобы это было заметно на дереве размером с LuCI.
Doctor предупреждает, когда рабочий каталог лежит под `/mnt`.

`fidelity: vm` здесь работает: WSL2 отдаёт `/dev/kvm`, и роутер грузится на
настоящем ядре на полной скорости.

### macOS

```console
$ go install owfeed.org/owlab/cmd/owlab@latest
$ brew install qemu        # только для fidelity: vm
$ owlab doctor
```

Docker Desktop, OrbStack, Colima или Rancher Desktop. На Apple Silicon
архитектура роутера по умолчанию aarch64, VM-tier идёт через hvf — загрузка за
девять секунд. Попросить там `x86_64` можно, но он будет транслироваться, и
doctor скажет об этом до того, как ждать.

### Windows

owlab — обычная консольная программа и запускается из PowerShell. Никакой
особой настройки под Windows, кроме `PATH`, не нужно.

```powershell
go install owfeed.org/owlab/cmd/owlab@latest
$env:PATH += ";$env:USERPROFILE\go\bin"        # на текущую сессию
owlab doctor
```

Чтобы сохранилось между сессиями — добавить каталог в «Параметры > Система > О
системе > Дополнительные параметры системы > Переменные среды», либо:

```powershell
[Environment]::SetEnvironmentVariable('PATH', "$env:PATH;$env:USERPROFILE\go\bin", 'User')
```

Без тулчейна Go — распаковать `owlab_<версия>_windows_<арх>.zip` из
[последнего релиза](https://github.com/owfeed/owlab/releases/latest) в
постоянное место и так же добавить каталог в `PATH`. Публикуются и `amd64`, и
`arm64`.

Docker Desktop с бэкендом WSL2. Контейнеры здесь линуксовые: Docker Desktop не
должен быть переключён в Windows containers, `docker version` обязан показывать
линуксовый сервер.

Для `fidelity: vm`:

```powershell
winget install SoftwareFreedomConservancy.QEMU
```

В `PATH` после этого ничего добавлять не нужно. Установщик туда QEMU не кладёт,
а owlab сам смотрит в `C:\Program Files\qemu` и в каталоги scoop и chocolatey;
`owlab doctor` печатает, какой бинарь он выбрал. Если у тебя он вообще в другом
месте — `OWLAB_QEMU=C:\путь\к\qemu-system-x86_64.exe`.

Дальше включить **Платформу гипервизора Windows** (Windows Hypervisor
Platform), она выключена по умолчанию. В PowerShell от администратора:

```powershell
dism.exe /Online /Enable-Feature /All /FeatureName:HypervisorPlatform
```

и перезагрузиться. Без неё у QEMU нет `whpx` и всё уходит в трансляцию — минуты
на загрузку вместо секунд. Doctor говорит, что из двух у тебя получилось, а не
оставляет засекать время.

Если VM не грузится вовсе или виснет на середине с включённым ускорением —
попробуй `$env:OWLAB_ACCEL="tcg"`. WHPX лежит поверх Hyper-V, и есть машины, на
которых он не работает; трансляция медленная, но работает всегда, и понять, с
чем из двух имеешь дело, — первое, что стоит установить.

Ещё две вещи doctor проверяет именно здесь, потому что обе — необязательные
компоненты Windows, а не данность:

- **Клиент OpenSSH**, в «Параметры > Система > Дополнительные компоненты». До
  роутеров `fidelity: vm` owlab добирается по ssh; до контейнерных — нет, так
  что проекту без VM он не нужен.
- **POSIX-шелл**, если у проекта задан `project.build`. Это командная строка
  шелла, и выполняет её `sh`; Git for Windows такой приносит. Ничем другим он не
  подменяется — отдать POSIX-строку в `cmd.exe` значит тихо выполнить не то.

Отдельно стоит посмотреть на переводы строк. Скрипты пакета исполняет busybox, а
он читает шебанг с CRLF как имя команды, оканчивающееся на `\r`, и сообщает
`bad interpreter: /bin/sh^M`. В `.gitattributes` проекта:

```
* text=auto eol=lf
```

и `git add --renormalize .`. Doctor ищет CRLF и падает на нём, потому что ничто
дальше по цепочке причину не назовёт.

Проверка, на любой из четырёх:

```console
$ owlab doctor
$ owlab up
$ curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8080/
200
```

---

## Начать работу над пакетом

```console
$ cd ~/src/luci-app-mine
$ owlab up
$ owlab open owrt2512
```

Проверка: LuCI отвечает, пункт меню вашего пакета на месте.

```console
$ curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8080/
200
```

Если `owlab.yaml` ещё нет, минимальный рабочий:

```yaml
version: 1
routers:
  - id: owrt2512
    release: "25.12.5"
```

---

## Цикл правок

```console
$ owlab sync --watch
```

Оставьте работать. Каждое сохранение копирует дерево на роутер и сбрасывает
кеши LuCI; перезагрузите страницу.

Проверка: правьте файл, видимый в браузере, дождитесь строки

```
  owrt2512   42 files, 846 KB
```

и обновите страницу.

Если пакет генерирует ассеты — `cascade.css` темы, скомпилированные переводы —
объявите сборку, иначе sync скопирует дерево без ровно того файла, который LuCI
и запрашивает:

```yaml
project:
  build: ./build-css.sh htdocs/luci-static/mytheme/cascade.css
```

---

## Проверить на другом релизе

Добавьте роутер и поднимите. Больше ничего менять не нужно.

```yaml
routers:
  - id: owrt2410
    release: "24.10.8"
```

```console
$ owlab up owrt2410
$ owlab sync owrt2410
```

Проверка: `owlab status` показывает `running`, и обратите внимание на колонку
`PKGMGR` — на 24.10 это opkg, на 25.12 apk. Эта разница и есть главная причина
держать вторую коробку.

---

## Узнать, не устарели ли пины

```console
$ owlab releases
ROUTER      DISTRO    PINNED    NEWEST
owrt2512    openwrt   25.12.4   25.12.5   1 release behind
owrt2410    openwrt   24.10.8   24.10.8   up to date
```

Сдвинуть пин: поправьте `release:` в `owlab.yaml`, затем

```console
$ owlab up --rebuild owrt2512
```

Проверка: `owlab releases` говорит up to date, и

```console
$ owlab exec owrt2512 -- 'ubus call system board | grep version'
```

показывает запиненный релиз.

Не пиньте `snapshot`. Snapshot-образы и snapshot-фиды пересобираются ежедневно
и независимо, поэтому установки начинают падать в течение суток.

---

## Поставить пакет попробовать

```console
$ owlab install owrt2512 luci-app-ttyd
```

Проверка: пункт меню появился после перезагрузки страницы.

`owlab up --rebuild` это не переживёт. Чтобы осталось — добавьте в `packages:`
через `+`:

```yaml
defaults:
  packages: ["+luci-app-ttyd"]
```

---

## Поставить свой пакет по URL

```yaml
defaults:
  extra_packages:
    - name: luci-app-mine
      apk: https://github.com/you/mine/releases/download/v1.0/luci-app-mine-1.0-r1.apk
      ipk: https://github.com/you/mine/releases/download/v1.0/luci-app-mine_1.0-r1_all.ipk
```

```console
$ owlab up --rebuild
```

Проверка: сборка печатает `owlab: installing luci-app-mine-1.0-r1.apk`. Провал
здесь фатален — сборка останавливается, а не отдаёт вам роутер, тихо лишённый
этого пакета.

Оба URL, потому что apk и opkg не делят схему именования. Пиньте точный релиз,
а не ссылку на `latest`: подписи здесь не проверяются.

---

## Собрать настоящий .apk или .ipk

```console
$ owlab build
$ owlab install owrt2512 dist/noarch/luci-app-mine-1.0-r1.apk
```

Проверка: файл лежит в `dist/`, и роутер отдаёт то же самое, что отдавал после
sync.

Стоит делать перед каждым релизом. Настоящая сборка минифицирует JS и CSS, а
sync — нет, и на этой разнице ломались пакеты, прекрасно работавшие на дев-боксе.

На Apple Silicon идёт под эмуляцией — все теги `openwrt/sdk` это `linux/amd64`
— так что будет медленно. owlab предупреждает до старта.

---

## Проверять пакет в CI

В репозитории пакета, без всякого `owlab.yaml`:

```yaml
- uses: owfeed/owlab/setup@v0.5.4
- run: owlab build --release 25.12.5 --out dist
- uses: owfeed/owlab/action@v0.5.4
  with:
    releases: "25.12.5 24.10.8"
    install: dist/*/luci-app-mine-*
    assert: |
      package luci-app-mine
      http 200 /cgi-bin/luci/admin/services/mine
```

Проверка: в summary джоба по строке на роутер, и сломанный пакет красит одну из
них в красный, называя упавшую проверку.

То же самое запускается локально перед пушем — это та же команда, и ей не нужно
ничего, кроме Docker:

```console
$ owlab test --release 25.12.5 --install 'dist/*/luci-app-mine-*.apk' \
    --assert 'http 200 /cgi-bin/luci/admin/services/mine'
```

Два релиза, а не один, потому что ломается именно эта ось: в 25.12 apk, в 24.10
opkg, артефакты называются по-разному, и LuCI внутри — не один и тот же LuCI.

`--keep` оставляет роутеры поднятыми, когда проверка упала и хочется посмотреть
на страницу глазами. В CI его не ставьте: снос — это то, что не даёт упавшему
прогону держать порты, нужные следующему.

Готовый workflow для копирования — в
[examples/workflow/package-ci.yml](../examples/workflow/package-ci.yml), все
флаги — в [справочнике](reference_ru.md#owlab-test).

---

## Работать с модулями ядра

Контейнер их не грузит. Нужен VM-роутер:

```yaml
routers:
  - id: real
    fidelity: vm
    release: "25.12.5"
    packages: ["+kmod-nft-tproxy"]
```

```console
$ owlab up real
$ owlab exec real -- 'uname -r; lsmod | grep nft_tproxy'
6.12.94
nft_tproxy    12288  0
```

Проверка: модуль в `lsmod`. В контейнере он ставится файлом и не грузится
никогда.

Первый старт — около полутора минут: скачивание, extroot, пакеты, две
перезагрузки. Дальше двадцать секунд. Диск переживает `owlab down`; сбрасывает
только `--rebuild`.

---

## Работать с WiFi

VM-роутер получает два радио `mac80211_hwsim`, и они настоящие.

```console
$ owlab exec real -- iwinfo
phy0-ap0  ESSID: "owlab"  Channel: 6 (2.437 GHz)  HT20  Tx-Power: 20 dBm
phy1-ap0  ESSID: "owlab"  Channel: 36 (5.180 GHz)         Tx-Power: 23 dBm
```

Проверка: `ubus call network.wireless status` показывает `"up": true` у обоих.

В контейнере вместо этого `fixtures: [wifi]` засевает конфиг. Меню, список
радио, SSID, режимы и вся форма редактирования отрисуются; сигнал, результаты
сканирования и таблица клиентов останутся пустыми — это приходит от настоящего
радио.

---

## Достучаться до сервиса, который не HTTP и не ssh

owlab пробрасывает только эти два. Остальное — форвардом через ssh:

```console
$ IP=$(owlab exec pk2512 -- 'uci get network.lan.ipaddr')
$ ssh -f -N -p 2295 -L 1080:$IP:2080 root@localhost
$ curl -x socks5h://127.0.0.1:1080 https://ifconfig.me
```

Проверка: адрес отличается от прямого `curl https://ifconfig.me`.

---

## Роутер перестал отвечать

```console
$ owlab status
$ owlab logs owrt2512 --tail 50
```

Дальше по убыванию вероятности:

```console
$ owlab exec owrt2512 -- 'service uhttpd status; netstat -lnt | grep :80'
$ owlab exec owrt2512 -- 'nft list chain inet fw4 input | head'
$ owlab exec owrt2512 -- 'ip route; ping -c1 -W3 8.8.8.8'
```

Роутер, который завершает TCP-рукопожатие и тут же сбрасывает соединение, а
uhttpd при этом жив и слушает — это
[flow offloading](troubleshooting_ru.md#роутер-сбрасывает-каждое-соединение).
Роутер, который отдаёт LuCI, но у которого висит любое исходящее соединение —
это [mwan3](troubleshooting_ru.md#luci-отвечает-но-исходящее-не-работает).

---

## Начать с нуля

```console
$ owlab up --rebuild          # выбросить роутеры и собрать заново
$ owlab down --purge          # и снести образы с дисками VM
```

`--rebuild` — это сброс к заводским. Всё, поставленное через `owlab install`,
любая правка uci руками и весь диск VM исчезают.

---

## Переехать на другую машину

Всё, что нужно owlab, — это `owlab.yaml` и ваше дерево исходников. Состояния за
пределами каталога проекта и одного кеша образов нет.

```console
$ owlab doctor
```

Проверка: ни одной строки `FAIL`. Предупреждения — это ограничения той машины,
а не ошибки; читайте их, там написано, что именно там не заработает.

---

## Указать конфиг в другом месте

```console
$ owlab --config ../other-project/owlab.yaml up
$ owlab --config ../other-project status        # каталог тоже подойдёт
$ export OWLAB_CONFIG=~/src/other-project
```

Работает и до имени команды, и после. Без него owlab ищет в текущем каталоге и
выше.

---

## Опубликовать образы, выпустить релиз

См. [релизы](releasing_ru.md).
