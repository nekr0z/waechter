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

package main

import (
	"bytes"
	"context"
	"database/sql/driver"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestWatcher(t *testing.T) {
	t.Parallel()
	want := "it works!"
	commands := []string{"echo " + want}
	ch, watchDir, log := setupWatcher(t, commands, nil, nil)

	writeFile(t, watchDir, "newfile")
	time.Sleep(time.Second)
	assertLog(t, log, want+"\n")

	writeFile(t, watchDir, "anotherfile")
	time.Sleep(time.Second)

	close(ch)

	assertLog(t, log, want+"\n"+want+"\n")
}

func TestStopWatcher(t *testing.T) {
	t.Parallel()
	commands := []string{
		"sleep 1",
		"echo done",
	}
	ch, watchDir, log := setupWatcher(t, commands, nil, nil)

	writeFile(t, watchDir, "file1")
	time.Sleep(time.Second)
	writeFile(t, watchDir, "file2") // this one will have no time to finish

	time.Sleep(time.Second)
	close(ch)

	time.Sleep(time.Second)

	assertLog(t, log, "done\n")
}

func TestWatcherQueuing(t *testing.T) {
	t.Parallel()
	commands := []string{
		"sleep 2",
		"echo done",
	}
	_, watchDir, log := setupWatcher(t, commands, nil, nil)

	writeFile(t, watchDir, "file1")
	time.Sleep(time.Second)
	writeFile(t, watchDir, "file2")
	time.Sleep(time.Second)
	writeFile(t, watchDir, "file1") // this one should not trigger a run, since a run is already queued
	time.Sleep(time.Second * 5)

	assertLog(t, log, "done\ndone\n")
}

func TestWatcherIncludeExclude(t *testing.T) {
	t.Parallel()

	include := []*regexp.Regexp{regexp.MustCompile(`.*\.go$`)}
	exclude := []*regexp.Regexp{regexp.MustCompile(`.+_test\.go$`)}

	watchDir, prep, mock := watchChanges(t, include, exclude)

	fn1 := "file.txt"
	fn2 := "file.go"
	fn3 := "file_test.go"

	fp2 := filepath.Join(watchDir, fn2)

	prep.ExpectExec().WithArgs(fp2, "create", AnyTime{}).WillReturnResult(sqlmock.NewResult(1, 1))

	writeFile(t, watchDir, fn1)
	time.Sleep(time.Second)

	writeFile(t, watchDir, fn2)
	time.Sleep(time.Second)

	writeFile(t, watchDir, fn3)
	time.Sleep(time.Second)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestReadConfig(t *testing.T) {
	t.Parallel()
	configFile := filepath.Join("testdata", "config.yaml")
	ww, dbConfig, err := readConfig(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(ww) != 2 {
		t.Fatalf("want len 1, got %v", len(ww))
	}

	want := "/home/user/project1"
	if ww[0].path != want {
		t.Errorf("path doesn't match: want %s, got %s", want, ww[0].path)
	}

	want = "go build -o ./build/bin/app1 cmd/service/main.go"
	if ww[0].commands[0] != want {
		t.Errorf("first command want: %s, got %s", want, ww[0].commands[0])
	}

	if len(ww[0].includeRe) != 2 {
		t.Errorf("want 2 includes, got %d", len(ww[0].includeRe))
	}

	if len(ww[0].excludeRe) != 1 {
		t.Errorf("want 1 exclude, got %d", len(ww[0].excludeRe))
	}

	want = "/home/user/project1_build_log.out"
	got := ww[0].logFile
	if got != want {
		t.Errorf("want %s, got %s", want, got)
	}

	want = "postgres://postgres:example@localhost:5432/postgres"
	got = dbConfig.Connection
	if got != want {
		t.Errorf("want %s, got %s", want, got)
	}
}

func TestReadConfigErr(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		file string
		len  int
		db   bool
	}{
		"full":         {"config.yaml", 2, true},
		"no db":        {"nodb.yaml", 1, false},
		"invalid":      {"invalid.yaml", 0, false},
		"invalid db":   {"invaliddb.yaml", 1, false},
		"missing file": {"nothing.atall", 0, false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join("testdata", tc.file)
			ww, db, err := readConfig(file)
			if len(ww) != tc.len {
				t.Errorf("want %d watchers, have %d: %s", tc.len, len(ww), ww)
			}
			if db.Kind.String() != "unknown" && !tc.db {
				t.Errorf("want no database, have %s", db)
			}
			if db.Kind.String() == "unknown" && tc.db {
				t.Errorf("want a DB config, got nil: %s", err)
			}
		})
	}
}

func TestMultipleCommands(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		commands []string
		want     string
	}{
		"normal run": {
			[]string{"echo hello world", "echo goodbye void"},
			"hello world\ngoodbye void\n",
		},
		"failing command": {
			[]string{"echo hello world", "exit 1", "echo goodbye world"},
			"hello world\n",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			runCommands(context.Background(), &output, tc.commands, nil)
			got := output.String()
			if tc.want != got {
				t.Fatalf("want:\n%s\ngot:\n%s\n", tc.want, got)
			}
		})
	}
}

func TestLogCommand(t *testing.T) {
	t.Parallel()
	commandsTable := "Exec"

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	prep := fmt.Sprintf("INSERT INTO %s\\(command, run_at\\) VALUES \\(\\$1, \\$2\\)", commandsTable)

	pr := mock.ExpectPrepare(prep)

	stmt, err := prepareCommandsStmt(db, commandsTable)
	if err != nil {
		t.Fatal(err)
	}

	commands := []string{"echo hello world", "echo goodbye void"}
	for i, cmd := range commands {
		pr.ExpectExec().WithArgs(EndString(cmd), AnyTime{}).WillReturnResult(sqlmock.NewResult(int64(i+1), 1))
	}

	runAndLogCommands(context.Background(), commands, stmt, "")

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestLogChanges(t *testing.T) {
	t.Parallel()

	watchDir, prep, mock := watchChanges(t, nil, nil)
	filename := filepath.Join(watchDir, "newfile")

	prep.ExpectExec().WithArgs(filename, "create", AnyTime{}).WillReturnResult(sqlmock.NewResult(1, 1))

	writeFile(t, watchDir, "newfile")

	time.Sleep(time.Second)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestParseRegexps(t *testing.T) {
	res := parseRegexps([]string{`\K`})
	if len(res) != 0 {
		t.Fatal("parsed an invalid regexp")
	}
}

func TestValidateTables(t *testing.T) {
	t.Parallel()

	a := []string{
		"some_table",
		"another_table",
		"yet_another_table",
		"you_guessed_it_another_table",
	}
	tests := map[string]struct {
		s1, s2       string
		want1, want2 bool
	}{
		"both":    {"yet_another_TABLE", "Some_Table", true, true},
		"first":   {"another_table", "that_table", true, false},
		"second":  {"nonono", "some_table", false, true},
		"neither": {"oh_no", "no_way", false, false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got1, got2 := validateTables(a, tc.s1, tc.s2)
			if !(got1 == tc.want1 && got2 == tc.want2) {
				t.Errorf("want %v-%v, got %v-%v", tc.want1, tc.want2, got1, got2)
			}
		})
	}
}

type AnyTime struct{}

// Match satisfies sqlmock.Argument interface
func (a AnyTime) Match(v driver.Value) bool {
	_, ok := v.(time.Time)
	return ok
}

type EndString string

// Match satisfies sqlmock.Argument interface
func (e EndString) Match(v driver.Value) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	return strings.HasSuffix(s, string(e))
}

func watchChanges(t *testing.T, include, exclude []*regexp.Regexp) (watchDir string, pr *sqlmock.ExpectedPrepare, mock sqlmock.Sqlmock) {
	changesTable := "Changes"
	watchDir = t.TempDir()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	prep := fmt.Sprintf("INSERT INTO %s\\(path, event, occured_at\\) VALUES \\(\\$1, \\$2, \\$3\\)", changesTable)

	pr = mock.ExpectPrepare(prep)

	stmt, err := prepareChangesStmt(db, changesTable)
	if err != nil {
		t.Fatal(err)
	}

	w := &watcher{
		path:      watchDir,
		includeRe: include,
		excludeRe: exclude,
	}

	ch, err := w.watch(stmt, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { close(ch) })
	return
}

func setupWatcher(t *testing.T, commands []string, include []*regexp.Regexp, exclude []*regexp.Regexp) (ch chan struct{}, watchingDir string, logFile string) {
	t.Helper()
	logDir := t.TempDir()
	logFile = filepath.Join(logDir, "log")
	watchingDir = t.TempDir()
	w := &watcher{
		path:     watchingDir,
		commands: commands,
		logFile:  logFile,
	}

	var err error
	ch, err = w.watch(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return
}

func writeFile(t *testing.T, dir string, filename string) {
	t.Helper()
	data := []byte("hello world")
	newFile := filepath.Join(dir, filename)
	err := os.WriteFile(newFile, data, 0644)
	if err != nil {
		t.Fatal(err)
	}
}

func assertLog(t *testing.T, logFile string, want string) {
	t.Helper()
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if got != want {
		t.Fatalf("want:\n%s\ngot:\n%s", want, got)
	}
}
