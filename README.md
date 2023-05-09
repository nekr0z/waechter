# Waechter
A CLI app to watch directories for changes and run chains of commands upon detecting a change.

![Build Status](https://github.com/nekr0z/waechter/actions/workflows/build.yml/badge.svg) [![codecov](https://codecov.io/gh/nekr0z/waechter/branch/master/graph/badge.svg)](https://codecov.io/gh/nekr0z/waechter)

##### Table of Contents
* [Installation and setup](#installation-and-setup)
  * [Compiling](#compiling) Waechter
  * [Configuration](#configuration)
* [Usage](#usage)
  * [Testing with a sample config](#testing-with-a-sample-configuration)
  * [Usage details](#usage-details)
* [Development](#development)
* [Credits](#credits)

## Installation and setup
The app requires no installation as such. If Go and Docker are installed in the system, you may [give Waechter a test run](#testing-with-a-sample-configuration) right away.

### Compiling
You can always download a precompiled binary from the [releases page](https://github.com/nekr0z/waechter/releases), but it's also perfectly OK to compile Waechter yourself. Provided that you have Go installed, all you need to do is:

    $ git clone https://github.com/nekr0z/waechter.git
    $ cd waechter
    $ go build


### Configuration

Waechter is configured by means of a [YAML](https://yaml.org/) file containing up to two documents (an example can be found [in this repository](testdata/config.yaml)). The first YAML document should include one or more directories to watch:

```yaml
- path: /home/user/project1
  commands:
    - go build -o ./build/bin/app1 cmd/service/main.go
    - go run ./build/bin/app1
  include_regexp:
    - .*\.go$
    - .*\.env$
  log_file: /home/user/project1_build_log.out
- path: ./testdata
  commands:
    - echo change detected
```

Each directory must have a valid `path` (either absolute or relative, but be careful with relative paths as those are resolved relative to the working directory at the time Waechter is run) and a list of `commands` to run. Note that the commands will be run directly, without spawning a shell, so if you need to do some shell scripting or piping, you'll need to wrap your commands in shell scripts as applicable.

The optional `log_file` parameter indicates the file to store the output of the executed commands (combined `stdout` and `stderr`). If no `log_file` is provided, the output is redirected to `stdout` of Waechter. `log_file` is always appended, so make sure to rotate logs as applicable.

The optional `include_regexp` is a list of regular expressions that filenames can be matched against. If the list is not empty, only changes in the files with names matching a regular expression from the list will be registered.

The optional second YAML document in the config file sets the database configuration:

```yaml
type: postgres
location: postgres://postgres:example@localhost:5432/postgres
changes: changes
commands: exec
```

The only supported `type` currently is "postgres", that is, a [PostgreSQL](https://www.postgresql.org/) database. `location` should contain a [connection string](https://www.postgresql.org/docs/current/libpq-connect.html#LIBPQ-CONNSTRING), both keyword/value and URI formats are supported. `changes` and `commands` are the names of the tables (one for storing the filesystem events, the other for storing the commands executed by Waechter). Each table can be omitted, in which case the corresponding data will not be stored.

The schema used for both tables can be found [here](postgres/init/create_tables.sql).

An example PostgreSQL installation is provided by means of a [Docker Compose](https://docs.docker.com/compose/) file. To run it, have Docker and Docker Compose installed, and from the cloned repository do:

    $ cd postrges
    $ sudo docker compose up

This will configure and run a PostgreSQL setup that corresponds to the configuration [in the example config file](testdata/config.yaml). The Adminer interface will be exposed at localhost:8080 where you can use `PostgreSQL` driver, `postgres` user with `example` password to access the `postgres` database and access the recorded entries.

## Usage

Run Waechter with

    $ waechter /path/to/config.yaml

(compiled version), or

    $ go run main.go /path/to/config.yaml

### Testing with a sample configuration

To test with the provided sample configuration, you may use the PostgreSQL Docker Compose setup (as explained above):

    $ git clone https://github.com/nekr0z/waechter.git
    $ cd waechter
    $ go build
    $ cd postgres
    $ sudo docker compose up -d
    $ cd ..
    $ waechter testdata/config.yaml

The current directory will be watched, and you can create, modify and delete files to trigger the commands. Issue a `SIGTERM` (by hitting `Ctrl+C`) to stop Waechter.

### Usage details

- if several commands are provided for a single path, they are run one by one in the order specified; if one of the commands exits with an error, the rest of the commands are not run;
- the directories are watched recursively; Waechter tries its best to add the newly created subdirectories recursively, too, but that has not been tested on all filesystems; 
- Waechter uses `inotify` to detect changes; the [usual issues](https://unix.stackexchange.com/questions/13751/kernel-inotify-watch-limit-reached) apply, so you may want to tweak your system accordingly;
- there's a 100 ms timer that is started whenever a change is detected; if a change occurs _on the same file_ before this timer expires, it is reset for another 100 ms; only after the file expires are the commands executed; this behaviour guards against the situations when a file is written to continuously in small chunks (such as when a Go binary is compiled), and it makes sense to wait for the whole write to finish before triggering the commands execution;
- if a set of commands is already running when a new change is detected, a new run is queued and will be performed as soon as the current run is finished; at most one "next" run will be queued, and the commands will not be executed concurrently; however, each path is watched separately, so whenever paths overlap (or several Waechter instances are run simultaneously), a single change can trigger concurrent commands execution;
- upon exiting (when `SIGTERM` is received) Waechter sends a `SIGKILL` to all the running commands;
- Waechter should work on any system it compiles on, but has only been tested in Linux.

## Development
Pull requests are always welcome!

## Credits
This software depends upon (and incorporates when compiled) the following software or parts thereof:
* [fsnotify](https://github.com/fsnotify/fsnotify) Copyright © 2012 The Go Authors. Copyright © fsnotify Authors.
* [go-yaml/yaml](https://gopkg.in/yaml) Copyright (c) 2006-2010 Kirill Simonov. Copyright (c) 2006-2011 Kirill Simonov. Copyright (c) 2011-2019 Canonical Ltd.
* [jackc/pgx](https://github.com/jackc/pgx) Copyright (c) 2013-2021 Jack Christensen.
* [The Go Programming Language](https://golang.org) Copyright © 2009 The Go Authors

Packages are built using [fpm](https://github.com/jordansissel/fpm) and [changelog](https://evgenykuznetsov.org/go/changelog).
