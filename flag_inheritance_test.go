// Copyright 2013-2023 The Cobra Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cobra

import (
	"reflect"
	"sort"
	"testing"

	"github.com/spf13/pflag"
)

// newFlagTestTree builds a fresh three-level command tree on every call:
//
//	root  (persistent flags: "rootflag", "shared")
//	└── mid   (persistent flags: "midflag", "shared")
//	    ├── leafa (local flags: "aflag", "shared")
//	    └── leafb (local flag:  "bflag")
//
// Every call returns new instances, so a test can mutate or execute its tree
// without affecting a tree built by another call.
func newFlagTestTree() (root, mid, leafa, leafb *Command) {
	root = &Command{Use: "root", Run: emptyRun}
	root.PersistentFlags().String("rootflag", "root-default", "persistent on root")
	root.PersistentFlags().String("shared", "root-shared", "persistent on root")

	mid = &Command{Use: "mid", Run: emptyRun}
	mid.PersistentFlags().String("midflag", "mid-default", "persistent on mid")
	mid.PersistentFlags().String("shared", "mid-shared", "persistent on mid")

	leafa = &Command{Use: "leafa", Run: emptyRun}
	leafa.Flags().String("aflag", "a-default", "local to leafa")
	leafa.Flags().String("shared", "leafa-shared", "local to leafa")

	leafb = &Command{Use: "leafb", Run: emptyRun}
	leafb.Flags().String("bflag", "b-default", "local to leafb")

	mid.AddCommand(leafa, leafb)
	root.AddCommand(mid)
	return root, mid, leafa, leafb
}

func sortedFlagNames(fs *pflag.FlagSet) []string {
	var names []string
	fs.VisitAll(func(f *pflag.Flag) { names = append(names, f.Name) })
	sort.Strings(names)
	return names
}

// TestThreeLevelFlagInheritance checks that flags declared on each level of a
// three-level tree resolve to the layer that owns them: a leaf command sees
// its ancestors' persistent flags as inherited, its own flags as local, and a
// value passed on the command line lands on the flag owned by the layer that
// declared it.
func TestThreeLevelFlagInheritance(t *testing.T) {
	root, mid, leafa, _ := newFlagTestTree()

	if got := leafa.InheritedFlags().Lookup("rootflag"); got == nil {
		t.Error("expected root persistent flag to be inherited by leafa")
	}
	if got := leafa.InheritedFlags().Lookup("midflag"); got == nil {
		t.Error("expected mid persistent flag to be inherited by leafa")
	}
	if got := leafa.InheritedFlags().Lookup("aflag"); got != nil {
		t.Error("local flag must not be reported as inherited")
	}
	if got := leafa.LocalFlags().Lookup("aflag"); got == nil {
		t.Error("expected local flag of leafa to be reported as local")
	}
	if got := leafa.LocalFlags().Lookup("rootflag"); got != nil {
		t.Error("inherited flag must not be reported as local")
	}

	_, err := executeCommand(root, "mid", "leafa", "--rootflag=r", "--midflag=m", "--aflag=a")
	assertNoErr(t, err)

	if got, _ := root.PersistentFlags().GetString("rootflag"); got != "r" {
		t.Errorf("expected root persistent flag to receive %q, got %q", "r", got)
	}
	if got, _ := mid.PersistentFlags().GetString("midflag"); got != "m" {
		t.Errorf("expected mid persistent flag to receive %q, got %q", "m", got)
	}
	if got, _ := leafa.Flags().GetString("aflag"); got != "a" {
		t.Errorf("expected leafa local flag to receive %q, got %q", "a", got)
	}
}

// TestLocalFlagShadowsParentPersistentFlag pins the rule for same-named
// flags: the executed command's own local flag wins. The shadowed persistent
// flags of the parents are neither set nor reported as inherited.
func TestLocalFlagShadowsParentPersistentFlag(t *testing.T) {
	root, mid, leafa, _ := newFlagTestTree()

	_, err := executeCommand(root, "mid", "leafa", "--shared=cli")
	assertNoErr(t, err)

	if got, _ := leafa.Flags().GetString("shared"); got != "cli" {
		t.Errorf("expected local flag to receive %q, got %q", "cli", got)
	}
	if got, _ := mid.PersistentFlags().GetString("shared"); got != "mid-shared" {
		t.Errorf("shadowed mid persistent flag changed to %q", got)
	}
	if got, _ := root.PersistentFlags().GetString("shared"); got != "root-shared" {
		t.Errorf("shadowed root persistent flag changed to %q", got)
	}

	if got := leafa.LocalFlags().Lookup("shared"); got == nil {
		t.Error("expected the effective shared flag to be leafa's local one")
	}
	if got := leafa.InheritedFlags().Lookup("shared"); got != nil {
		t.Error("shadowed parent flag must not be reported as inherited")
	}
}

// TestNearestParentPersistentFlagWins pins the rule for same-named persistent
// flags on multiple ancestors when the leaf declares no local flag with that
// name: the nearest parent's persistent flag is the effective one.
func TestNearestParentPersistentFlagWins(t *testing.T) {
	root, mid, _, leafb := newFlagTestTree()

	_, err := executeCommand(root, "mid", "leafb", "--shared=cli")
	assertNoErr(t, err)

	if got, _ := mid.PersistentFlags().GetString("shared"); got != "cli" {
		t.Errorf("expected nearest parent's persistent flag to receive %q, got %q", "cli", got)
	}
	if got, _ := root.PersistentFlags().GetString("shared"); got != "root-shared" {
		t.Errorf("shadowed root persistent flag changed to %q", got)
	}

	if got := leafb.LocalFlags().Lookup("shared"); got != nil {
		t.Error("leafb declares no shared flag; it must not be reported as local")
	}
	if got := leafb.InheritedFlags().Lookup("shared"); got == nil {
		t.Error("expected the effective shared flag to be inherited by leafb")
	}
}

// TestLocalFlagsDoNotLeakToSiblingOrParent checks that a local flag declared
// on one command stays on that command: siblings can neither see nor parse
// it, and parents never have it added to any of their flag sets, even after
// the owning command has been executed.
func TestLocalFlagsDoNotLeakToSiblingOrParent(t *testing.T) {
	root, mid, _, leafb := newFlagTestTree()

	_, err := executeCommand(root, "mid", "leafa", "--aflag=x")
	assertNoErr(t, err)

	if got := leafb.Flags().Lookup("aflag"); got != nil {
		t.Error("local flag of leafa leaked into sibling's flag set")
	}
	if _, err := executeCommand(root, "mid", "leafb", "--aflag=x"); err == nil {
		t.Error("expected an unknown flag error when passing leafa's local flag to leafb")
	}

	for name, cmd := range map[string]*Command{"root": root, "mid": mid} {
		if got := cmd.PersistentFlags().Lookup("aflag"); got != nil {
			t.Errorf("local flag of leafa leaked into %s's persistent flags", name)
		}
		if got := cmd.Flags().Lookup("aflag"); got != nil {
			t.Errorf("local flag of leafa leaked into %s's flags", name)
		}
	}
}

// TestFlagResolutionDoesNotMutateParents checks that resolving flags for a
// command never modifies the flag sets of its parents. In particular the
// process-wide pflag.CommandLine set must be resolvable from every command
// without being injected into the root command's persistent flags.
func TestFlagResolutionDoesNotMutateParents(t *testing.T) {
	pflag.Bool("processflag", false, "registered on pflag.CommandLine")
	defer resetCommandLineFlagSet()

	root, mid, _, leafb := newFlagTestTree()

	_, err := executeCommand(root, "mid", "leafb", "--processflag")
	assertNoErr(t, err)

	if got := leafb.Flags().Lookup("processflag"); got == nil {
		t.Error("expected pflag.CommandLine flag to be resolvable from leafb")
	}
	if got := leafb.InheritedFlags().Lookup("processflag"); got == nil {
		t.Error("expected pflag.CommandLine flag to be reported as inherited by leafb")
	}

	for name, cmd := range map[string]*Command{"root": root, "mid": mid} {
		if got := cmd.PersistentFlags().Lookup("processflag"); got != nil {
			t.Errorf("%s's persistent flags polluted with a pflag.CommandLine flag", name)
		}
	}
	if got, want := sortedFlagNames(root.PersistentFlags()), []string{"rootflag", "shared"}; !reflect.DeepEqual(got, want) {
		t.Errorf("root persistent flags changed during child execution: expected %v, got %v", want, got)
	}
	if got, want := sortedFlagNames(mid.PersistentFlags()), []string{"midflag", "shared"}; !reflect.DeepEqual(got, want) {
		t.Errorf("mid persistent flags changed during child execution: expected %v, got %v", want, got)
	}
}

// TestRootMergesCommandLineFlagsLocally checks that the root command resolves
// pflag.CommandLine flags through its own full flag set while its persistent
// flag set stays exactly what the application declared.
func TestRootMergesCommandLineFlagsLocally(t *testing.T) {
	pflag.Bool("rootprocessflag", false, "registered on pflag.CommandLine")
	defer resetCommandLineFlagSet()

	root, _, _, _ := newFlagTestTree()

	_, err := executeCommand(root, "--rootprocessflag")
	assertNoErr(t, err)

	if got, _ := root.Flags().GetBool("rootprocessflag"); !got {
		t.Error("expected pflag.CommandLine flag to be parsed on the root command")
	}
	if got := root.PersistentFlags().Lookup("rootprocessflag"); got != nil {
		t.Error("root's persistent flags polluted with a pflag.CommandLine flag")
	}
}

// TestAddCommandDoesNotModifyFlagSets checks that assembling the command
// tree is a pure registration step: linking a child to its parent touches
// neither command's flag sets.
func TestAddCommandDoesNotModifyFlagSets(t *testing.T) {
	parent := &Command{Use: "parent", Run: emptyRun}
	parent.PersistentFlags().String("pflag", "", "persistent on parent")
	child := &Command{Use: "child", Run: emptyRun}
	child.Flags().String("cflag", "", "local to child")

	parent.AddCommand(child)

	if got, want := sortedFlagNames(parent.Flags()), []string(nil); !reflect.DeepEqual(got, want) {
		t.Errorf("AddCommand modified parent's flags: expected %v, got %v", want, got)
	}
	if got, want := sortedFlagNames(parent.PersistentFlags()), []string{"pflag"}; !reflect.DeepEqual(got, want) {
		t.Errorf("AddCommand modified parent's persistent flags: expected %v, got %v", want, got)
	}
	if got, want := sortedFlagNames(child.Flags()), []string{"cflag"}; !reflect.DeepEqual(got, want) {
		t.Errorf("AddCommand modified child's flags: expected %v, got %v", want, got)
	}
	if got, want := sortedFlagNames(child.PersistentFlags()), []string(nil); !reflect.DeepEqual(got, want) {
		t.Errorf("AddCommand modified child's persistent flags: expected %v, got %v", want, got)
	}
}

// TestNewTreesAreIndependent checks that a freshly constructed tree carries
// no state over from a tree built by the same constructor, so tests can
// always start from a clean tree.
func TestNewTreesAreIndependent(t *testing.T) {
	root1, _, _, _ := newFlagTestTree()
	root2, _, _, _ := newFlagTestTree()

	_, err := executeCommand(root1, "mid", "leafb", "--shared=cli", "--rootflag=r1")
	assertNoErr(t, err)

	if got, _ := root2.PersistentFlags().GetString("shared"); got != "root-shared" {
		t.Errorf("second tree affected by execution of first tree: shared is %q", got)
	}
	if got, _ := root2.PersistentFlags().GetString("rootflag"); got != "root-default" {
		t.Errorf("second tree affected by execution of first tree: rootflag is %q", got)
	}
}
