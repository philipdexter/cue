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

type completer struct{}

func (*completer) Do(line []rune, pos int) ([][]rune, int) {

	// TODO make better, since this will
	// match 'a :_'
	if pos > 0 && line[pos-1] == ':' {
		return [][]rune{
			[]rune("help"),
			[]rune("l"),
			[]rune("lookup"),
			[]rune("p"),
			[]rune("print"),
		}, 1
	}

	out := make([][]rune, 0)

	var r readline.Runes
	if len(r.TrimSpaceLeft(line)) == 0 {
		out = append(out, [][]rune{
			[]rune(":help"),
			[]rune(":l"),
			[]rune(":lookup"),
			[]rune(":p"),
			[]rune(":print"),
		}...)
	}

	// TODO make better, right now just always try
	// to match a top level field label
	bii, err := buildI()
	if err != nil {
		return nil, 0
	}

	if strct, err := bii.Value().Struct(); err == nil {
		iter := strct.Fields()
		for iter.Next() {
			out = append(out, []rune(iter.Label()))
		}
	}

	return out, 0
}

func runRepl(cmd *Command, args []string) error {
	fmt.Println("Welcome to the CUE repl")

	usr, err := user.Current()
	if err != nil {
		panic(err)
	}
	homeDir := usr.HomeDir
	cueConfigDir := filepath.Join(homeDir, ".config", "cue")
	if _, err := os.Stat(cueConfigDir); os.IsNotExist(err) {
		err := os.MkdirAll(cueConfigDir, 0755)
		if err != nil {
			fmt.Println(err)
		}
	}

	rl, err := readline.NewEx(&readline.Config{
		Prompt:            "\033[34m>\033[0m ",
		HistoryFile:       filepath.Join(cueConfigDir, ".hist"),
		HistorySearchFold: true,
		EOFPrompt:         "^D",
		InterruptPrompt:   "^C",
		AutoComplete:      &completer{},
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
			var err error
			if strings.HasPrefix(text, "@") {
				err = addExpr(strings.TrimPrefix(text, "@"))
			} else {
				err = evalExpr(text)
			}
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

func evalExpr(expr string) error {

	astExpr, err := parser.ParseExpr("", expr)
	if err != nil {
		return err
	}

	bii, err := buildI()
	if err != nil {
		return err
	}
	val := bii.Eval(astExpr)
	if val.Err() != nil {
		return val.Err()
	}
	return pprint(val)
}

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
	// TODO cache results, if nothing has changed
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
		if len(commandParts) != 2 {
			fmt.Println(":inject takes one argument")
			return nil
		}
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
		if len(commandParts) != 1 {
			fmt.Println(":print takes no arguments")
			return nil
		}
		bii, err := buildI()
		if err != nil {
			return err
		}
		err = pprint(bii.Value())
		if err != nil {
			return err
		}
	} else if command == "l" || command == "lookup" {
		if len(commandParts) != 2 {
			fmt.Println(":lookup requires one argument")
			return nil
		}
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
