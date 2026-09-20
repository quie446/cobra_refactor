package cobra

import (
	"testing"

	flag "github.com/spf13/pflag"
)

// newThreeLevelTree builds a root -> mid -> leaf tree where "shared" is
// declared on every persistent/local layer:
//
//	root  persistent "shared" (default "root-p")
//	mid   persistent "shared" (default "mid-p")
//	leaf  local      "shared" (default "leaf-l")
//
// leaf additionally gets a leaf-only local flag, and root gets a sibling of
// mid to verify local flags never leak sideways.
func newThreeLevelTree() (root, mid, sibling, leaf *Command) {
	root = &Command{Use: "root"}
	root.PersistentFlags().String("shared", "root-p", "root persistent flag")

	mid = &Command{Use: "mid"}
	mid.PersistentFlags().String("shared", "mid-p", "mid persistent flag")

	leaf = &Command{
		Use: "leaf",
		Run: emptyRun,
	}
	leaf.Flags().String("leafonly", "leaf-default", "leaf local flag")
	leaf.Flags().String("shared", "leaf-l", "leaf local flag")

	sibling = &Command{Use: "sibling", Run: emptyRun}

	mid.AddCommand(leaf)
	root.AddCommand(mid, sibling)
	return root, mid, sibling, leaf
}

// A child accessing its flags must never mutate the persistent flag set of any
// of its parents. This pins down the boundary between owned and inherited
// persistent flags.
func TestChildFlagsDoNotPolluteParentPersistentFlags(t *testing.T) {
	root, mid, _, leaf := newThreeLevelTree()

	// Touching the leaf's effective flag set must not write root/mid flags.
	if f := leaf.Flags().Lookup("shared"); f == nil {
		t.Fatal("leaf should resolve the inherited/shared flag")
	}
	if root.PersistentFlags().Lookup("leafonly") != nil {
		t.Error("leaf local flag leaked into root PersistentFlags")
	}
	if mid.PersistentFlags().Lookup("leafonly") != nil {
		t.Error("leaf local flag leaked into mid PersistentFlags")
	}

	leaf.PersistentFlags().String("leafpersist", "x", "")
	_ = leaf.Flags()
	if root.PersistentFlags().Lookup("leafpersist") != nil {
		t.Error("leaf persistent flag leaked into root PersistentFlags")
	}
	if mid.PersistentFlags().Lookup("leafpersist") != nil {
		t.Error("leaf persistent flag leaked into mid PersistentFlags")
	}

	// A freshly created, unrelated tree is completely independent.
	fresh := &Command{Use: "fresh"}
	if fresh.PersistentFlags().Lookup("leafpersist") != nil {
		t.Error("flag from one tree appeared on an unrelated command tree")
	}
}

// Flags declared on pflag.CommandLine stay an implicit bottom layer: they are
// visible to every command for parsing, but never become part of a command's
// own PersistentFlags set.
func TestCommandLineFlagsDoNotPollutePersistentFlags(t *testing.T) {
	resetCommandLineFlagSet()
	t.Cleanup(resetCommandLineFlagSet)
	flag.String("globalflag", "g", "")

	root := &Command{Use: "root", Run: emptyRun}
	child := &Command{Use: "child", Run: emptyRun}
	root.AddCommand(child)

	if root.LocalFlags().Lookup("globalflag") == nil {
		t.Error("root must resolve pflag.CommandLine flags via LocalFlags()")
	}
	if child.InheritedFlags().Lookup("globalflag") == nil {
		t.Error("child must resolve pflag.CommandLine flags via InheritedFlags()")
	}
	if root.PersistentFlags().Lookup("globalflag") != nil {
		t.Error("pflag.CommandLine flag was copied into root PersistentFlags")
	}
	if root.InheritedFlags().Lookup("globalflag") != nil {
		t.Error("root must not list pflag.CommandLine flags as inherited")
	}
	if root.LocalFlags().Lookup("globalflag") == nil {
		t.Error("root must classify pflag.CommandLine flags as local, as before")
	}
}

// When the same flag name exists on several layers, the declaration closest to
// the executed command wins: local > own persistent > nearest parent
// persistent > root persistent.
func TestSameNameFlagPrecedence(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected string
	}{
		{
			name:     "local flag on leaf wins over all persistent layers",
			args:     []string{"mid", "leaf"},
			expected: "leaf-l",
		},
		{
			name:     "command line value wins over every default",
			args:     []string{"mid", "leaf", "--shared", "cli"},
			expected: "cli",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root, _, _, leaf := newThreeLevelTree()
			var got string
			leaf.Run = func(c *Command, _ []string) {
				got, _ = c.Flags().GetString("shared")
			}

			if _, err := executeCommand(root, tc.args...); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}

// Without a local shadow, the nearest parent's persistent flag wins over the
// same-named persistent flag declared higher up.
func TestNearestParentPersistentFlagWins(t *testing.T) {
	root := &Command{Use: "root"}
	root.PersistentFlags().String("shared", "root-p", "")

	mid := &Command{Use: "mid"}
	mid.PersistentFlags().String("shared", "mid-p", "")

	var gotRoot, gotMid string
	leaf := &Command{
		Use: "leaf",
		Run: func(c *Command, _ []string) {
			v, _ := c.Flags().GetString("shared")
			gotMid = v
		},
	}

	rootRunner := &Command{
		Use: "printroot",
		Run: func(c *Command, _ []string) {
			v, _ := c.Flags().GetString("shared")
			gotRoot = v
		},
	}
	mid.AddCommand(leaf)
	root.AddCommand(mid, rootRunner)

	if _, err := executeCommand(root, "mid", "leaf"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMid != "mid-p" {
		t.Errorf("leaf must see nearest parent's value, got %q", gotMid)
	}

	if _, err := executeCommand(root, "printroot"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotRoot != "root-p" {
		t.Errorf("root child must see root persistent value, got %q", gotRoot)
	}
}

// Across three levels, each closer declaration shadows the farther one even
// when no value comes from the command line.
func TestThreeLevelPersistentShadowDefaults(t *testing.T) {
	root := &Command{Use: "root"}
	root.PersistentFlags().String("depth", "root", "")

	mid := &Command{Use: "mid"}
	mid.PersistentFlags().String("depth", "mid", "")

	leafP := &Command{
		Use: "leafp",
		Run: emptyRun,
	}
	leafP.PersistentFlags().String("depth", "leafp", "")

	var seenByMid, seenByLeafP, seenByPlainLeaf string
	mid.Run = func(c *Command, _ []string) {
		seenByMid, _ = c.Flags().GetString("depth")
	}
	leafP.Run = func(c *Command, _ []string) {
		seenByLeafP, _ = c.Flags().GetString("depth")
	}

	plainLeaf := &Command{
		Use: "leaf",
		Run: func(c *Command, _ []string) {
			seenByPlainLeaf, _ = c.Flags().GetString("depth")
		},
	}

	mid.AddCommand(leafP, plainLeaf)
	root.AddCommand(mid)

	if _, err := executeCommand(root, "mid"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seenByMid != "mid" {
		t.Errorf("mid must shadow root persistent flag, got %q", seenByMid)
	}

	if _, err := executeCommand(root, "mid", "leafp"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seenByLeafP != "leafp" {
		t.Errorf("leafp own persistent flag must win, got %q", seenByLeafP)
	}

	if _, err := executeCommand(root, "mid", "leaf"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seenByPlainLeaf != "mid" {
		t.Errorf("plain leaf must inherit the nearest parent default, got %q", seenByPlainLeaf)
	}

	// The farther default never appears in the winner's flag definition.
	if f := leafP.Flags().Lookup("depth"); f == nil || f.DefValue != "leafp" {
		t.Errorf("leafp effective flag definition must be its own, got %+v", f)
	}
}

// A local flag declared on one command must neither persist to children of a
// sibling branch nor appear on the sibling itself.
func TestLocalFlagsDoNotLeakToSibling(t *testing.T) {
	root, _, sibling, leaf := newThreeLevelTree()

	// "leafonly" belongs to leaf only.
	if leaf.LocalFlags().Lookup("leafonly") == nil {
		t.Error("leaf must report leafonly as local")
	}
	if sibling.LocalFlags().Lookup("leafonly") != nil {
		t.Error("leafonly leaked to sibling command")
	}
	if sibling.Flags().Lookup("leafonly") != nil {
		t.Error("sibling effective Flags() contain leafonly")
	}

	// A child of the sibling is a different branch and must not inherit it.
	nephew := &Command{Use: "nephew", Run: emptyRun}
	sibling.AddCommand(nephew)
	if nephew.Flags().Lookup("leafonly") != nil {
		t.Error("leafonly leaked across branches to nephew")
	}
	if nephew.InheritedFlags().Lookup("leafonly") != nil {
		t.Error("leafonly appears as inherited flag on nephew")
	}

	// Shared persistent flags still flow down every branch.
	if sibling.InheritedFlags().Lookup("shared") == nil {
		t.Error("sibling must still inherit root's persistent shared flag")
	}

	// And the local flag is unknown on the command line for the sibling.
	if _, err := executeCommand(root, "sibling", "--leafonly", "x"); err == nil {
		t.Error("expected unknown-flag error when using leafonly on sibling")
	}
}

// Moving a command under a new parent recomputes its inherited flags.
func TestReparentingRefreshesInheritedFlags(t *testing.T) {
	rootA := &Command{Use: "rootA"}
	rootA.PersistentFlags().String("fromA", "a", "")
	rootB := &Command{Use: "rootB"}
	rootB.PersistentFlags().String("fromB", "b", "")

	child := &Command{Use: "child", Run: emptyRun}
	rootA.AddCommand(child)

	if child.InheritedFlags().Lookup("fromA") == nil {
		t.Fatal("child must inherit from its first parent")
	}
	if child.InheritedFlags().Lookup("fromB") != nil {
		t.Fatal("child must not inherit from an unrelated tree")
	}

	rootA.RemoveCommand(child)
	if child.InheritedFlags().Lookup("fromA") != nil {
		t.Error("detached child must drop its old inherited flags")
	}

	rootB.AddCommand(child)
	if child.InheritedFlags().Lookup("fromA") != nil {
		t.Error("child must not retain flags from the old parent")
	}
	if child.InheritedFlags().Lookup("fromB") == nil {
		t.Error("child must inherit flags from the new parent")
	}
}

// Built-in command assembly can be performed explicitly and is idempotent:
// assembling twice produces the same tree instead of churning it.
func TestInitDefaultCommandsIsExplicitAndIdempotent(t *testing.T) {
	root := &Command{Use: "root"}
	userCmd := &Command{Use: "do", Run: emptyRun}
	root.AddCommand(userCmd)

	// Pretend a completion request is being made so the hidden __complete
	// command is kept attached (it is otherwise only registered on demand).
	root.InitDefaultCommands(ShellCompNoDescRequestCmd, "do", "")

	var helpCmd, compCmd, completeCmd *Command
	for _, sub := range root.commands {
		switch sub.Name() {
		case helpCommandName:
			helpCmd = sub
		case compCmdName:
			compCmd = sub
		case ShellCompRequestCmd:
			completeCmd = sub
		}
	}
	if helpCmd == nil {
		t.Error("explicit assembly did not register the help command")
	}
	if compCmd == nil {
		t.Error("explicit assembly did not register the completion command")
	}
	if completeCmd == nil {
		t.Error("explicit assembly did not register the __complete command")
	}

	countAfterFirst := len(root.commands)
	root.InitDefaultCommands(ShellCompNoDescRequestCmd, "do", "")
	root.InitDefaultHelpCmd()
	if len(root.commands) != countAfterFirst {
		t.Errorf("repeated assembly changed command count: %d -> %d",
			countAfterFirst, len(root.commands))
	}

	secondPass := false
	for _, sub := range root.commands {
		if sub.Name() == helpCommandName {
			if secondPass {
				t.Fatal("help command registered more than once")
			}
			if sub != helpCmd {
				t.Error("help command was rebuilt instead of being reused")
			}
			secondPass = true
		}
	}

	// Shell subcommands of the completion command keep being available.
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		if cmd, _, err := root.Find([]string{compCmdName, shell}); err != nil || cmd == nil {
			t.Errorf("completion %s subcommand not assembled: %v", shell, err)
		}
	}
}

// Repeated executions of the same tree must not rebuild or reorder its
// built-in commands.
func TestRepeatedExecuteKeepsTreeStable(t *testing.T) {
	root := &Command{Use: "root"}
	root.AddCommand(&Command{Use: "do", Run: emptyRun})

	if _, err := executeCommand(root, "do"); err != nil {
		t.Fatalf("first execute failed: %v", err)
	}
	helpAfterFirst := root.helpCommand

	orderAfterFirst := make([]string, len(root.commands))
	for i, sub := range root.commands {
		orderAfterFirst[i] = sub.Name()
	}

	if _, err := executeCommand(root, "do"); err != nil {
		t.Fatalf("second execute failed: %v", err)
	}
	if root.helpCommand != helpAfterFirst {
		t.Error("help command instance changed between executions")
	}
	if len(root.commands) != len(orderAfterFirst) {
		t.Fatal("command count changed between executions")
	}
	for i, sub := range root.commands {
		if sub.Name() != orderAfterFirst[i] {
			t.Fatalf("command order changed: %v -> %v", orderAfterFirst, commandNames(root.commands))
		}
	}
}

func commandNames(cmds []*Command) []string {
	names := make([]string, len(cmds))
	for i, c := range cmds {
		names[i] = c.Name()
	}
	return names
}
