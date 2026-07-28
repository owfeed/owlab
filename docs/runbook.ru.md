# Ранбук

[English](runbook.md)

Процедуры. Каждая — задача, команды и способ убедиться, что вышло.

Что означает ключ — в [справочнике](reference.ru.md). Почему оно так себя ведёт
— во [внутренностях](internals.ru.md). Когда что-то сломалось — в
[диагностике](troubleshooting.ru.md), она отсортирована по симптомам.

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
- uses: VizzleTF/owlab/setup@v0.3.0
- run: owlab build --release 25.12.5 --out dist
- uses: VizzleTF/owlab/action@v0.3.0
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
флаги — в [справочнике](reference.ru.md#owlab-test).

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
[flow offloading](troubleshooting.ru.md#роутер-сбрасывает-каждое-соединение).
Роутер, который отдаёт LuCI, но у которого висит любое исходящее соединение —
это [mwan3](troubleshooting.ru.md#luci-отвечает-но-исходящее-не-работает).

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

См. [релизы](releasing.ru.md).
