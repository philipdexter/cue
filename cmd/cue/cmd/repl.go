package cmd

import (
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/chzyer/readline"
	"github.com/spf13/cobra"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/build"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/load"
	"cuelang.org/go/cue/parser"
)

func newReplCmd(c *Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repl",
		Short: "Start a repl",
		Long: `Starts a repl in the current directory.

If the current directory is a module then the repl
will read the module files. If there is no module
in the current directory then the repl starts in freestyle mode.`,
		RunE: mkRunE(c, runRepl),
	}
	return cmd
}

var autoComplete = readline.NewPrefixCompleter(
	readline.PcItem(":help"),
	readline.PcItem(":l"),
	readline.PcItem(":lookup"),
	readline.PcItem(":p"),
	readline.PcItem(":print"),
)

func runRepl(cmd *Command, args []string) error {
	fmt.Println("Welcome to the CUE repl")

	usr, err := user.Current()
	if err != nil {
		panic(err)
	}
	homeDir := usr.HomeDir
	cueConfigDir := filepath.Join(homeDir, ".config", "cue", ".hist")
	// TODO mkdir cueConfigDir

	rl, err := readline.NewEx(&readline.Config{
		Prompt:            "\033[34m>\033[0m ",
		HistoryFile:       cueConfigDir,
		HistorySearchFold: true,
		EOFPrompt:         "^D",
		InterruptPrompt:   "^C",
		AutoComplete:      autoComplete,
	})
	if err != nil {
		panic(err)
	}
	defer rl.Close()

	mod, inMod := Module()
	if inMod {
		fmt.Println("(running in module " + mod + ")")
	} else {
		fmt.Println("(running in freestyle mode)")
	}

	for {
		line, err := rl.Readline()
		if err == readline.ErrInterrupt {
			if len(line) == 0 {
				break
			} else {
				continue
			}
		} else if err == io.EOF {
			break
		}

		text := strings.TrimSpace(line)
		if text == "exit" {
			break
		}

		if strings.HasPrefix(text, ":") {
			err := execCommand(text)
			if err != nil {
				fmt.Println(err)
			}
		} else {
			err := addExpr(text)
			if err != nil {
				fmt.Println(err)
			}
		}
	}

	fmt.Println("bye")

	return nil
}

//////////////////////

var r cue.Runtime

var bi *build.Instance

var inModule = false

func addExpr(expr string) error {
	// TODO maybe try to build here, so we can
	// catch errors earlier

	// TODO somehow call inst.ctxt.parseFunc if possible?

	if len(bi.Files) > 0 {
		astF, err := parser.ParseFile("", expr)
		if err != nil {
			return err
		}
		for _, decl := range astF.Decls {
			bi.Files[0].Decls = append(bi.Files[0].Decls, decl)
		}
		for _, imp := range astF.Imports {
			bi.Files[0].Imports = append(bi.Files[0].Imports, imp)
		}
		for _, un := range astF.Unresolved {
			bi.Files[0].Unresolved = append(bi.Files[0].Unresolved, un)
		}

		return nil
	} else {
		return bi.AddFile("repl", expr)
	}
}

func buildI() (*cue.Instance, error) {
	return r.Build(bi)
}

func pprint(v cue.Value) error {
	bytes, err := format.Node(v.Syntax())
	if err != nil {
		return err
	}
	fmt.Println(strings.TrimSpace(string(bytes)))
	return nil
}

func resetI() {
	if inModule {
		bis := load.Instances([]string{}, nil)
		if len(bis) != 1 {
			panic("len(bis) != 1")
		}
		bi = bis[0]
	} else {
		bi = &build.Instance{}
	}
}

func Module() (string, bool) {
	if inModule {
		return bi.Module, true
	}

	return "", false
}

func init() {
	_, err := os.Stat("cue.mod")
	inModule = !os.IsNotExist(err)

	resetI()
}

func execCommand(text string) error {
	commandParts := strings.Fields(strings.TrimPrefix(text, ":"))
	command := commandParts[0]
	if command == "i" || command == "inject" {
		file := commandParts[1]
		err := bi.AddFile(file, nil)
		if err != nil {
			return err
		}
		return nil
	} else if command == "help" {
		// TODO update help
		fmt.Println("help -- print help text")
		fmt.Println("l/lookup <path> -- print the value at <path>")
		fmt.Println("p/print -- print the current value")
	} else if command == "p" || command == "print" {
		bii, err := buildI()
		if err != nil {
			return err
		}
		err = pprint(bii.Value())
		if err != nil {
			return err
		}
	} else if command == "l" || command == "lookup" {
		bii, err := buildI()
		if err != nil {
			return err
		}
		ii := bii.Value().Lookup(strings.Split(commandParts[1], ".")...)
		if !ii.Exists() {
			fmt.Println("error: no value")
		}
		err = pprint(ii)
		if err != nil {
			return err
		}
	}
	return nil
}
