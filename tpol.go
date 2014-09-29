package main

import (
	"fmt"
	"github.com/GeertJohan/go.linenoise"
	"github.com/firba1/complete"
	"io/ioutil"
	"os"
	"os/exec"
	"os/signal"
	"strings"
)

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
		func(args string) (completions []string) {
			completions = complete.Bash(cmdname + " " + args)
			for i, v := range completions {
				completions[i] = v[len(cmdname)+1:]
			}
			return
		},
	)

	fmt.Println("shell for", cmdpath)

	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, os.Interrupt)
	for {
		promptStr := ps1(cmdname)
		line, err := linenoise.Line(fmt.Sprintf("%v>%v ", promptStr, cmdname))
		if err != nil {
			fmt.Println(err.Error())
			return
		}
		linenoise.AddHistory(line)

		var cmd *exec.Cmd
		// escape to shell
		if len(line) >= 1 && line[0] == '!' {
			cmdFields := strings.Fields(line[1:])
			cmd = exec.Command(cmdFields[0], cmdFields[1:]...)
		} else if strings.TrimSpace(line) == "exit" {
			return
		} else {
			args := strings.Fields(line)
			cmd = exec.Command(cmdname, args...)
		}

		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin

		cancel := make(chan int)
		go sigintCatcher(sigint, cancel, cmd)
		err = cmd.Run()
		close(cancel)

		if err != nil {
			fmt.Println(err.Error())
			continue
		}
	}
}

func sigintCatcher(sigint chan os.Signal, cancel chan int, cmd *exec.Cmd) {
	select {
	case <-cancel:
		return
	case <-sigint:
		cmd.Process.Signal(os.Interrupt)
	}
}

func ps1(cmdname string) (promptStr string) {
	if cmdname == "git" {
		cmd := exec.Command("bash", "-l", "-c", "__git_ps1")
		gitPs1Stdout, err := cmd.StdoutPipe()
		if err != nil {
			fmt.Println(err.Error())
		}

		if err := cmd.Start(); err != nil {
			fmt.Println(err.Error())
		}

		ps1Bytes, err := ioutil.ReadAll(gitPs1Stdout)
		if err != nil {
			fmt.Println(err.Error())
		}
		promptStr = strings.TrimSpace(string(ps1Bytes))

		if err := cmd.Wait(); err != nil {
			fmt.Println(err.Error())
		}
	}
	return
}
