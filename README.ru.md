# Waechter
Консольное приложение для отслеживания изменений в директориях и запуска команд при обнаружении таких изменений.

![Build Status](https://github.com/nekr0z/waechter/actions/workflows/build.yml/badge.svg) [![codecov](https://codecov.io/gh/nekr0z/waechter/branch/master/graph/badge.svg)](https://codecov.io/gh/nekr0z/waechter)

##### Содержание
* [Установка и настройка](#установка-и-настройка)
  * [Компиляция](#компиляция)
  * [Настройка](#настройка)
* [Использование](#использование)
  * [Проверка работы с примером конфигурации](#проверка-работы-с-примером-конфигурации)
  * [Особенности работы](#особенности-работы)
* [Разработка](#разработка)
* [При создании использованы](#при-создании-использованы)

## Установка и настройка

Собственно, установка как таковая приложению не требуется. При наличии в системе Go и Docker можно сразу [протестировать работу приложения](#testing-with-a-sample-configuration).

### Компиляция
На [странице с релизами](https://github.com/nekr0z/waechter/releases) доступны скомпилированные файлы, но можно и скомпилировать Waechter самостоятельно. Если в системе установлены Go и Git, необходимо просто:

    $ git clone https://github.com/nekr0z/waechter.git
    $ cd waechter
    $ go build


### Настройка

Waechter конфигурируется [YAML](https://yaml.org/)-файлом, который может содержать один или два документа(пример конфигурации есть [в этом репозитории](testdata/config.yaml)). Первый документ YAML должен указывать на одну или несколько директорий, за изменениями в которых Waechter будет наблюдать:

```yaml
- path: /home/user/project1
  commands:
    - go build -o ./build/bin/app1 cmd/service/main.go
    - go run ./build/bin/app1
  include_regexp:
    - .*\.go$
    - .*\.env$
  exclude_regexp:
    - .+_test\.go$
  log_file: /home/user/project1_build_log.out
- path: ./testdata
  commands:
    - echo change detected
```

Для каждой директории нужно указать путь (`path`) — абсолютный или относительный (относительные пути будут отсчитываться от текущей рабочей директории в момент запуска Waechter) — и список команд (`commands`), которые нужно запускать. Примите во внимание, что команды запускаются не в оболочке, так что при необходимости использовать перенаправление придётся «завернуть» команды в скрипт.

Параметр `log_file` необязателен, он указывает на имя файла, в который будет сохраняться вывод запускаемых команды (объединённый, `stdout` и `stderr`). Если этот параметр опущен, вывод будет перенаправлен в `stdout` самой программы Waechter. Файл с выводом каждый раз дополняется, а не пишется заново; пользователь должен самостоятельно следить за его размером.

Необязательный параметр `include_regexp` позволяет задать список регулярных выражений для имён файлов. Если этот список не пуст, только файлы с именами, отвечающими одному из выражений списка, будут учитываться при отслеживании изменений. Невалидные выражения (например, `\K`) игнорируются; список, состоящий только из невалидных выражений, расценивается как пустой (т.е. никакие файлы не игнорируются).

Аналогично, необязательный параметр `exclude_regexp` задаёт список регулярных выражений для игнорирования файлов. Если имя файла отвечает и выражению из `include_regexp`, и выражению из `exclude_regexp`, такой файл будет проигнорирован.

Второй YAML-документ не обязателен; в нём указывается конфигурация базы данных:

```yaml
type: postgres
location: postgres://postgres:example@localhost:5432/postgres
changes: changes
commands: exec
```

Единственный тип (`type`), который пока поддерживается — "postgres", т.е. база данных [PostgreSQL](https://www.postgresql.org/). Параметр `location` должен содержать [строку соединения](https://www.postgresql.org/docs/current/libpq-connect.html#LIBPQ-CONNSTRING), возможно использование как формата «ключ/значение», так и URI. Параметры `changes` и `commands` задают названия таблиц для хранения журналов событий файловой системы и запущенных Waechter команд, соответственно. Если одна из таблиц не указана, соответствующий журнал не будет сохраняться.

Схему для обеих таблиц можно найти [здесь](postgres/init/create_tables.sql).

В репозитории есть пример настройки PostgreSQL средствами [Docker Compose](https://docs.docker.com/compose/). Для запуска убедитесь, что в системе установлены Docker и Docker Compose, и из склонированного репозитория выполните:

    $ cd postrges
    $ sudo docker compose up

Будет запущен сервис PostgreSQL, настроенный так, как указано в [примере конфигурации](testdata/config.yaml). На localhost:8080 будет запущен Adminer, в котором можно выбрать драйвер `PostgreSQL`, задать имя пользователя `postgres` с паролем `example` для доступа к базе `postgres` и посмотреть на сохранённые журналы.

## Использование

Waechter можно запустить так:

    $ waechter /path/to/config.yaml

(если это скомпилированный файл), или так:

    $ go run main.go /path/to/config.yaml

### Проверка работы с примером конфигурации

Для проверки с прилагаемым примером настроек можно использовать PostgreSQL, настроенный в Docker Compose (как описано выше):

    $ git clone https://github.com/nekr0z/waechter.git
    $ cd waechter
    $ go build
    $ cd postgres
    $ sudo docker compose up -d
    $ cd ..
    $ waechter testdata/config.yaml

Текущая директория будет отслеживаться, и можно создавать, изменять и удалять файлы, чтобы вызвать запуск команд. Для прекращения работы пошлите `SIGTERM` (нажатием `Ctrl+C`), чтобы остановить Waechter.

### Особенности работы

- если для директории настроено несколько команд, они запускаются одна за другой в том порядке, в котором перечислены в файле конфигурации; если одна из команд завершается с ошибкой, остальные команды не будут запущены;
- изменения в директориях отслеживаются рекурсивно для всех поддиректорий; при создании новых поддиректорий Waechter будет пытаться наблюдать за ними (тоже рекурсивно), но этот функционал протестирован не для всех файловых систем; 
- Waechter использует `inotify` для отслеживания изменений; это может приводить к [известным проблемам](https://unix.stackexchange.com/questions/13751/kernel-inotify-watch-limit-reached) и потребовать соответствующей настройки системы;
- при обнаружении изменения в директории запускается таймер длительностью 100 мс; если _тот же файл_ снова изменяется до истечения этого времени, таймер сбрасывается и отсчитывает новые 100 мс; так сделано для того, чтобы в ситуации, когда запись в файл ведётся небольшими порциями (например, при компиляции приложения на Go), не запускать команды, пока такая запись не завершена;
- если новое изменение в директории обнаружено, когда последовательность команд запущена и ещё не завершилась, новый запуск последовательности команд ставится в очередь и будет выполнен, когда завершится выполнение уже запущенной последовательности; в очереди может находиться только один новый запуск (т.о. серия изменений, пока выполняется длинная команда, не приведёт к постановке в очередь нескольких запусков); следует иметь в виду, что каждая из заданных директорий отслеживается отдельно, и если две из настроенных директорий «перекрываются» (или если запущено несколько копий Waechter), одно изменение в файловой системе может запустить параллельно несколько последовательностей команд;
- регулярные выражения в `exclude_regexp` и `include_regexp` проверяются на соответствие пути в целом (абсолютному или относительному, в зависимости от того, как задан параметр `path`), а не одному только имени файла;
- при выходе (при получении `SIGTERM`) Waechter отправляет `SIGKILL` всем запущенным командам;
- Waechter, теоретически, должен работать на любой системе, для которой может быть скомпилирован, но проверялась работа только на Linux.

## Разработка
Пулл-реквесты приветствуются!

## При создании использованы

(и при компиляции включаются в состав приложения полностью или частично):

* [fsnotify](https://github.com/fsnotify/fsnotify) Copyright © 2012 The Go Authors. Copyright © fsnotify Authors.
* [go-yaml/yaml](https://gopkg.in/yaml) Copyright (c) 2006-2010 Kirill Simonov. Copyright (c) 2006-2011 Kirill Simonov. Copyright (c) 2011-2019 Canonical Ltd.
* [jackc/pgx](https://github.com/jackc/pgx) Copyright (c) 2013-2021 Jack Christensen.
* [The Go Programming Language](https://golang.org) Copyright © 2009 The Go Authors

Пакеты собираются с использованием [fpm](https://github.com/jordansissel/fpm) и [changelog](https://evgenykuznetsov.org/go/changelog).
