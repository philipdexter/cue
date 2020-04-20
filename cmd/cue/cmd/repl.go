package cmd

import (
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
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

		if strings.HasPrefix(text, ";") {
			err := ExecCommand(text)
			if err != nil {
				fmt.Println(err)
			}
		} else {
			err := AddExpr(text)
			if err != nil {
				fmt.Println(err)
			}
			err = CheckpointHistory()
			if err != nil {
				// TODO undo AddExpr and CheckpointHistory
				fmt.Println(err)
			}
		}
	}

	fmt.Println("bye")

	return nil
}

//////////////////////

var r cue.Runtime

var history []cue.Value // Maybe store the built instance if build returns a new one every time
var bi *build.Instance

var inModule = false

func AddExpr(expr string) error {
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

func CheckpointHistory() error {
	bii, err := BuildI()
	if err != nil {
		return err
	}
	history = append(history, bii.Value())
	return nil
}

func BuildI() (*cue.Instance, error) {
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

	bii, err := BuildI()
	if err != nil {
		panic(err)
	}

	history = make([]cue.Value, 0)
	history = append(history, bii.Value())
}

func ExecCommand(text string) error {
	commandParts := strings.Fields(strings.TrimPrefix(text, ";"))
	command := commandParts[0]
	if command == "i" || command == "inject" {
		file := commandParts[1]
		err := bi.AddFile(file, nil)
		if err != nil {
			return err
		}
		return CheckpointHistory()
	} else if command == "help" {
		fmt.Println("help -- print help text")
		fmt.Println("l/lookup <path> -- print the value at <path>")
		fmt.Println("h/history -- print history indexed by histnums")
		fmt.Println("p/print -- print the current value")
		fmt.Println("r/restore <histnum> -- restore the value at <histnum>")
		fmt.Println("s/save <where> -- save the current value into <where>")
	} else if command == "s" || command == "save" {
		i, err := r.Compile("repl", ``)
		if err != nil {
			return err
		}
		newv := i.Value()
		newv = newv.Fill(history[len(history)-1], strings.Split(commandParts[1], ".")...)
		if newv.Err() != nil {
			return newv.Err()
		}
		resetI()
		bytes, err := format.Node(newv.Syntax())
		if err != nil {
			return err
		}
		err = AddExpr(string(bytes))
		if err != nil {
			return err
		}
		return CheckpointHistory()
	} else if command == "r" || command == "restore" {
		i, err := strconv.ParseInt(commandParts[1], 10, 64)
		if err != nil {
			return err
		}
		resetI()
		bytes, err := format.Node(history[i].Syntax())
		if err != nil {
			return err
		}
		err = AddExpr(string(bytes))
		if err != nil {
			return err
		}
	} else if command == "h" || command == "history" {
		fmt.Printf("\033[34m====\033[0m\n")
		for i, vv := range history {
			fmt.Printf("\033[34m%d\033[0m\n", i)
			err := pprint(vv)
			if err != nil {
				return err
			}
			if i != len(history)-1 {
				fmt.Printf("\033[34m----\033[0m\n")
			}
		}
		fmt.Printf("\033[34m====\033[0m\n")
	} else if command == "p" || command == "print" {
		bii, err := BuildI()
		if err != nil {
			return err
		}
		err = pprint(bii.Value())
		if err != nil {
			return err
		}
	} else if command == "l" || command == "lookup" {
		bii, err := BuildI()
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
