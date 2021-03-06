package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/GeertJohan/go.linenoise"
	"github.com/firba1/complete"
)

const escapeCharacter = '!'
const configDirName = ".tpol"
const logsDirName = "logs"
const historyDirName = "history"
const promptStringConfigFilename = "prompts.json"

func getPromptStringConfigPath() (string, error) {
	usr, err := user.Current()
	if err != nil {
		return "", err
	}

	return filepath.Join(
		usr.HomeDir,
		configDirName,
		promptStringConfigFilename,
	), nil
}

func getPromptStringConfig() (mappings map[string]string, err error) {
	mappings = make(map[string]string)

	promptStringConfigPath, err := getPromptStringConfigPath()
	if err != nil {
		return
	}

	fd, err := os.Open(promptStringConfigPath)
	if err != nil {
		return
	}
	defer fd.Close()

	err = json.NewDecoder(fd).Decode(&mappings)
	if err != nil {
		mappings = make(map[string]string)
		return
	}

	return
}

func getHistoryDir() (string, error) {
	dirList := []string{configDirName, historyDirName}
	usr, err := user.Current()
	if err == nil {
		dirList = append([]string{usr.HomeDir}, dirList...)
	}
	return strings.Join(dirList, "/"), err
}

func getLogsDir() (string, error) {
	usr, err := user.Current()
	if err != nil {
		return "", err
	}
	return filepath.Join(usr.HomeDir, configDirName, logsDirName), nil
}

func getLogger(cmdname string) *log.Logger {
	logsDir, err := getLogsDir()
	if err != nil {
		fmt.Println(err)
	}
	err = os.MkdirAll(logsDir, os.ModeDir|0744)
	if err != nil {
		fmt.Println(err)
	}
	logpath := filepath.Join(logsDir, fmt.Sprintf("%v-%v.log", cmdname, time.Now().Format(time.RFC3339)))
	var logwriter io.Writer
	logfile, err := os.Create(logpath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Unable to create log file: ", err)
		logwriter = os.Stderr
	} else {
		logwriter = logfile
	}
	return log.New(logwriter, "", log.Ldate|log.Ltime)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Printf("usage:\t%v COMMAND\n", os.Args[0])
		return
	}

	cmdname := os.Args[1]

	cmdpath, err := exec.LookPath(cmdname)
	if err != nil {
		fmt.Println(err.Error())
		return
	}

	linenoise.SetCompletionHandler(
		func(args string) []string {
			cmdFilter := CommandFilter{}
			if len(args) > 1 && args[0] == byte(escapeCharacter) {
				cmdFilter = CommandFilter{
					func(s string) string {
						return s[1:]
					},
					func(s string) string {
						return string(escapeCharacter) + s
					},
				}
			} else {
				prefix := cmdname + " "
				cmdFilter = CommandFilter{
					func(s string) string {
						return prefix + s
					},
					func(s string) string {
						return s[len(prefix):]
					},
				}
			}
			return completions(args, cmdFilter)
		},
	)

	promptStringConfig, _ := getPromptStringConfig()
	if _, ok := promptStringConfig["git"]; !ok {
		// set default for git
		promptStringConfig["git"] = "__git_ps1"
	}
	promptStringMappings := make([]PromptStringMapping, 0, len(promptStringConfig))
	for command, promptFunc := range promptStringConfig {
		promptStringMappings = append(
			promptStringMappings,
			PromptStringMapping{
				Command: command,
				Func:    promptFunc,
			},
		)
	}
	ps := NewPromptStringer(promptStringMappings...)

	fmt.Println("shell for", cmdpath)

	historyDir, err := getHistoryDir()
	if err != nil {
		fmt.Println(err)
	}
	err = os.MkdirAll(historyDir, os.ModeDir|0744)
	if err != nil {
		fmt.Println(err)
	}

	historyPath := strings.Join([]string{historyDir, cmdname}, "/")
	err = linenoise.LoadHistory(historyPath)
	if err != nil {
		fmt.Println("No history file found: new histoy file created at " + historyPath)
	} else {
		fmt.Println("Using history file at: " + historyPath)
	}

	logger := getLogger(cmdname)

	for {
		promptStr := ps.PromptString(cmdname)
		line, err := linenoise.Line(fmt.Sprintf("%v>%v ", promptStr, cmdname))
		if err != nil {
			logger.Println(err.Error(), line)
			break
		}

		var cmd *exec.Cmd
		if len(line) >= 1 && line[0] == '!' { // escape to shell
			cmdFields := strings.Fields(line[1:])
			cmd = exec.Command(cmdFields[0], cmdFields[1:]...)
		} else if strings.TrimSpace(line) == "exit" { // exit
			break
		} else if strings.TrimSpace(line) == "" { // don't run anything
			continue
		} else { // regular subcommand
			args := strings.Fields(line)
			if len(args) > 0 && args[0] == cmdname {
				// if the user types the command again (common mistake I make), ignore it
				args = args[1:]
			}
			cmd = exec.Command(cmdname, args...)
			linenoise.AddHistory(line) // add history iff non-metacommand
		}

		// hook up expecting tty stuff
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin

		// handle interrupts
		cancel := catchAndPassSignal(cmd, os.Interrupt)

		err = cmd.Run()
		close(cancel)

		if err != nil {
			logger.Println(err.Error(), cmd)
			continue
		}
	}
	err = linenoise.SaveHistory(historyPath)
	if err != nil {
		fmt.Println(err)
	}
}

/*
catchAndPassSignal catches the given signals and passes them to the process of the given command

catchAndPassSignal can be canceled by closing the cancel channel that it returns
*/
func catchAndPassSignal(cmd *exec.Cmd, signals ...os.Signal) (cancel chan int) {
	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, signals...)

	cancel = make(chan int)

	go func() {
		select {
		case <-cancel:
			return
		case sig := <-sigint:
			cmd.Process.Signal(sig)
		}
	}()
	return
}

type CommandFilter struct {
	Filter   func(string) string
	Unfilter func(string) string
}

func completions(line string, filter CommandFilter) []string {
	completionList := complete.Complete(filter.Filter(line))

	for i, v := range completionList {
		completionList[i] = filter.Unfilter(v)
	}
	return completionList
}
