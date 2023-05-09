// Copyright (C) 2023 Evgeny Kuznetsov (evgeny@kuznetsov.md)
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

//go:generate go run version_generate.go

package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	_ "github.com/jackc/pgx/v5/stdlib"
	"gopkg.in/yaml.v3"
)

var (
	cooldownTime = time.Millisecond * 100

	version string = "custom"
)

// watcher watches the directory recursively and fires the commands.
type watcher struct {
	Path     string // dir to watch
	Commands []string
	LogFile  string `yaml:"log_file,omitempty"` // if empty, use stdout
}

// dbConfig holds data required to store the events and commands logs
type dbConfig struct {
	Kind       dbDriver `yaml:"type"`
	Connection string   `yaml:"location"`
	Changes    string   `yaml:"changes,omitempty"`  // table to store changes: Path VARCHAR(4096), Event VARCHAR(64), OccuredAt TIMESTAMP
	Commands   string   `yaml:"commands,omitempty"` // table to store commands: Command VARCHAR(4096), RunAt TIMESTAMP
}

type dbDriver string

const (
	postgres dbDriver = "postgres"
)

// event represents a change on the filesystem.
type event struct {
	path string
	kind eventKind
}

type eventKind int

const (
	chmod eventKind = iota
	create
	write
	remove
	rename
)

// watch starts the watcher and provides a channel to signal quit.
func (w watcher) watch(changesStmt, commandsStmt *sql.Stmt) (chan struct{}, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	adder := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsDir() {
			return fw.Add(path)
		}
		return nil
	}

	if err := filepath.Walk(w.Path, adder); err != nil {
		fw.Close()
		return nil, err
	}

	ch := make(chan struct{})

	// this ensures that only one command sequence is queued and run after the
	// already running one completes
	runCh := make(chan struct{}, 1)
	exitCh := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	go execute(runCh, ctx, w.Commands, commandsStmt, w.LogFile)

	var (
		mu     sync.Mutex
		timers = make(map[string]*time.Timer)

		run = func(e event) {
			logEvent(e, changesStmt)
			mu.Lock()
			delete(timers, e.path)
			mu.Unlock()
			select {
			case <-exitCh:
				close(runCh)
			case runCh <- struct{}{}:
			default:
			}
		}
	)

	go func() {
		defer fw.Close()
		for {
			select {
			case ev := <-fw.Events:
				e := parseEvent(ev)
				if e.kind == chmod { // makes sense to ignore
					continue
				}
				if e.kind == create {
					// if a directory is created, we'd better watch it
					if info, err := os.Lstat(e.path); err == nil && info.Mode().IsDir() {
						_ = filepath.Walk(e.path, adder)
					}
				}
				mu.Lock()
				t, ok := timers[e.path]
				mu.Unlock()

				if !ok {
					t = time.AfterFunc(cooldownTime, func() { run(e) })
					t.Stop()
					mu.Lock()
					timers[e.path] = t
					mu.Unlock()
				}
				t.Reset(cooldownTime)
			case <-ch:
				cancel()
				close(exitCh)
				<-runCh
				<-runCh
				return
			}
		}
	}()

	return ch, nil
}

// execute runs commands upon receiving a signal, ensuring that only one set of
// commands is run concurrently
func execute(ch <-chan struct{}, ctx context.Context, commands []string, stmt *sql.Stmt, logFile string) {
	for range ch {
		runAndLogCommands(ctx, commands, stmt, logFile)
	}
}

func parseCommand(ctx context.Context, in string) *exec.Cmd {
	cc := strings.Split(in, " ")
	cmd := exec.CommandContext(ctx, cc[0], cc[1:]...)
	// only since Go 1.20
	// (*cmd).Cancel = cmd.Process.Signal(syscall.SIGTERM)
	return cmd
}

func runAndLogCommands(ctx context.Context, cmds []string, commandStmt *sql.Stmt, logTarget string) {
	logTo := os.Stdout
	if logTarget != "" {
		var err error
		logTo, err = os.OpenFile(logTarget, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			log.Printf("could not open log file %s: %s", logTarget, err)
			logTo = os.Stdout
		} else {
			defer logTo.Close()
		}
	}
	runCommands(ctx, logTo, cmds, commandStmt)
	_ = logTo.Sync()
}

func runCommands(ctx context.Context, w io.Writer, cmds []string, commandsStmt *sql.Stmt) {
	for _, command := range cmds {
		c := parseCommand(ctx, command)
		if commandsStmt != nil {
			_, err := commandsStmt.Exec(c.String(), time.Now().UTC())
			if err != nil {
				panic(err)
			}
		}

		out, err := c.CombinedOutput()

		if _, err := w.Write(out); err != nil {
			log.Printf("error writing to log: %s", err)
		}

		if err != nil {
			break
		}
	}
}

func logEvent(e event, changesStmt *sql.Stmt) {
	if changesStmt == nil {
		return
	}
	_, err := changesStmt.Exec(e.path, e.kind.String(), time.Now().UTC())
	if err != nil {
		panic(err)
	}
}

func readConfig(filename string) ([]watcher, *dbConfig, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, nil, fmt.Errorf("could not open config file: %w", err)
	}

	dec := yaml.NewDecoder(f)
	var out []watcher
	err = dec.Decode(&out)
	if err != nil {
		return nil, nil, fmt.Errorf("could not parse watcher config: %w", err)
	}

	var db dbConfig
	err = dec.Decode(&db)
	if err != nil {
		err = fmt.Errorf("could not parse DB config: %w", err)
	}
	return out, &db, err
}

func initDB(conf *dbConfig) (changesStmt, commandsStmt *sql.Stmt) {
	none := "will not store event and command logs"
	if conf == nil {
		log.Printf("no database config, %s", none)
		return
	}
	if conf.Kind.String() == "unknown" {
		log.Printf("database type unknown, %s", none)
		return
	}
	db, err := sql.Open(conf.Kind.String(), conf.Connection)
	if err != nil {
		log.Printf("failed to initialize database, %s: %s", none, err)
		return
	}

	tables := getTableNames(db)
	changesOk, commandsOk := validateTables(tables, conf.Changes, conf.Commands)

	if !changesOk {
		log.Println("will not store event log: no such table")
	} else {
		changesStmt, err = prepareChangesStmt(db, conf.Changes)
		if err != nil {
			log.Printf("will not store event log: %s", err)
		}
	}

	if !commandsOk {
		log.Println("will not store command log: no such table")
	} else {
		commandsStmt, err = prepareCommandsStmt(db, conf.Commands)
		if err != nil {
			log.Printf("will not store command log: %s", err)
		}
	}
	return
}

func getTableNames(db *sql.DB) []string {
	var out []string
	rows, err := db.Query("SELECT table_name FROM information_schema.tables WHERE table_schema='public' AND table_type='BASE TABLE';")
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		err := rows.Scan(&name)
		if err != nil {
			return out
		}
		out = append(out, name)
	}
	return out
}

func validateTables(a []string, name1, name2 string) (bool, bool) {
	var ok1, ok2 bool
	for _, s := range a {
		if !ok1 && strings.EqualFold(s, name1) {
			ok1 = true
		}
		if !ok2 && strings.EqualFold(s, name2) {
			ok2 = true
		}
	}
	return ok1, ok2
}

func prepareCommandsStmt(db *sql.DB, cmdTable string) (*sql.Stmt, error) {
	if db == nil {
		return nil, fmt.Errorf("no database provided")
	}
	if cmdTable == "" {
		return nil, fmt.Errorf("no table specified")
	}
	stmt, err := db.Prepare(fmt.Sprintf("INSERT INTO %s(command, run_at) VALUES ($1, $2)", cmdTable))
	return stmt, err
}

func prepareChangesStmt(db *sql.DB, changesTable string) (*sql.Stmt, error) {
	if db == nil {
		return nil, fmt.Errorf("no database provided")
	}
	if changesTable == "" {
		return nil, fmt.Errorf("no table specified")
	}
	stmt, err := db.Prepare(fmt.Sprintf("INSERT INTO %s(path, event, occured_at) VALUES ($1, $2, $3)", changesTable))
	return stmt, err
}

func (e eventKind) String() string {
	switch e {
	case create:
		return "create"
	case write:
		return "write"
	case rename:
		return "rename"
	case remove:
		return "delete"
	default:
		return "unknown"
	}
}

func parseEvent(fe fsnotify.Event) event {
	e := event{
		path: fe.Name,
	}
	if fe.Op.Has(fsnotify.Write) {
		e.kind = write
	} else if fe.Op.Has(fsnotify.Create) {
		e.kind = create
	} else if fe.Op.Has(fsnotify.Remove) {
		e.kind = remove
	} else if fe.Op.Has(fsnotify.Rename) {
		e.kind = rename
	}
	return e
}

func (d dbDriver) String() string {
	switch d {
	case postgres:
		return "pgx"
	default:
		return "unknown"
	}
}

func setupSignalHandling(done chan<- struct{}, channels []chan<- struct{}) {
	stopSig := make(chan os.Signal, 1)
	signal.Notify(stopSig, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	go func() {
		<-stopSig
		fmt.Println("\nAborting...")
		for _, ch := range channels {
			close(ch)
		}
		close(done)
	}()
}

func main() {
	fmt.Printf("Waechter version %s\n", version)

	if len(os.Args) != 2 {
		fmt.Println("Mandatory config file name not provided.")
		os.Exit(1)
	}

	watchers, dbConfig, err := readConfig(os.Args[1])
	if err != nil {
		log.Println(err)
	}

	changesStmt, commandsStmt := initDB(dbConfig)

	var running ([]chan<- struct{})
	for _, w := range watchers {
		ch, err := w.watch(changesStmt, commandsStmt)
		if err != nil {
			log.Printf("couldn't begin watching %s: %s", w.Path, err)
		} else {
			running = append(running, ch)
		}
	}

	done := make(chan struct{}, 1)
	setupSignalHandling(done, running)
	<-done
}
