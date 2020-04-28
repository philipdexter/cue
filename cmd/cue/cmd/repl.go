package cmd

import (
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	goruntime "runtime"
	"runtime/debug"
	"strings"

	"github.com/chzyer/readline"
	"github.com/spf13/cobra"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/build"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/load"
	"cuelang.org/go/cue/parser"
)

const (
	flagLoadModule flagName = "load-module"
)

const (
	defaultPrompt   = "> "
	multilinePrompt = "@ "
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

	cmd.Flags().BoolP(string(flagLoadModule), "m", true,
		"Autoload a module from the current directory")

	return cmd
}

type completer struct{}

func (*completer) Do(line []rune, pos int) ([][]rune, int) {

	// TODO make better, since this will
	// match 'a :_'
	if pos > 0 && line[pos-1] == ':' {
		return [][]rune{
			[]rune("help"),
			[]rune("p"),
			[]rune("print"),
			[]rune("@"),
		}, 1
	}

	out := make([][]rune, 0)

	var r readline.Runes
	if len(r.TrimSpaceLeft(line)) == 0 {
		out = append(out, [][]rune{
			[]rune(":help"),
			[]rune(":p"),
			[]rune(":print"),
			[]rune(":@"),
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
	var (
		mod   = ""
		inMod = false
	)
	if flagLoadModule.Bool(cmd) {
		if initModule() {
			inMod = true
			mod = bi.Module
		}
	}

	version := defaultVersion
	if bi, ok := debug.ReadBuildInfo(); ok && version == defaultVersion {
		// No specific version provided via version
		version = bi.Main.Version
	}
	fmt.Printf("cue version %v %s/%s\n",
		version,
		goruntime.GOOS,
		goruntime.GOARCH)

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
		Prompt:                 "> ",
		HistoryFile:            filepath.Join(cueConfigDir, ".hist"),
		HistorySearchFold:      true,
		EOFPrompt:              "^D",
		InterruptPrompt:        "^C",
		AutoComplete:           &completer{},
		DisableAutoSaveHistory: true,
	})
	if err != nil {
		panic(err)
	}
	defer rl.Close()

	if inMod {
		fmt.Println("(running in module " + mod + ")")
	} else {
		fmt.Println("(running in freestyle mode)")
	}
	fmt.Println("Type ':help' for help (type ^C or ^D to exit)")

	var (
		lines        []string
		multiline    bool
		wasMultiline bool
	)
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

		line = strings.TrimRight(line, " \t\n")
		if len(line) == 0 {
			continue
		}

		wasMultiline = false
		if line == ":@" {
			if multiline {
				multiline = false
				wasMultiline = true
				line = ""
				rl.SetPrompt(defaultPrompt)
			} else {
				multiline = true
				rl.SetPrompt(multilinePrompt)
				continue
			}
		}

		lines = append(lines, line)

		if multiline {
			continue
		}

		if !wasMultiline && strings.HasPrefix(line, ":") {
			err := execCommand(line)
			if err != nil {
				fmt.Println(err)
			}
		} else {
			var err error
			if wasMultiline || strings.HasPrefix(line, "@") {
				err = addStmt(strings.TrimPrefix(strings.Join(lines, "\n"), "@"))
				// TODO if the statment had an error, "undo" the addstmt
				// (probably requires building)
			} else {
				err = evalExpr(line)
			}
			if err != nil {
				fmt.Println(err)
			}
		}

		if len(lines) > 1 {
			rl.SaveHistory(strings.Join(lines, "\n"))
		} else {
			rl.SaveHistory(strings.Join(lines, ""))
		}
		lines = lines[:0]
	}

	fmt.Println("bye")

	return nil
}

//////////////////////

var r cue.Runtime

var bi = &build.Instance{}

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

func addStmt(expr string) error {
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

func initModule() bool {
	_, err := os.Stat("cue.mod")
	if !os.IsNotExist(err) {
		bis := load.Instances([]string{}, nil)
		if len(bis) != 1 {
			panic("len(bis) != 1")
		}
		bi = bis[0]
		return true
	}
	return false
}

func execCommand(text string) error {
	commandParts := strings.Fields(strings.TrimPrefix(text, ":"))
	if len(commandParts) == 0 {
		return nil
	}
	command := commandParts[0]

	if command == "help" {
		// TODO update help
		fmt.Println("help -- print help text")
		fmt.Println("p/print -- print the current value")
		return nil
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
		return nil
	}

	return fmt.Errorf("unknown command %s", text)
}
