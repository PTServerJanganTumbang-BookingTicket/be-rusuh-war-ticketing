// Command speccheck parses every Gherkin .feature file with the official
// Cucumber parser and enforces the tag rules in docs/DEVELOPMENT-FLOW.md.
//
//	speccheck [-step N [-dirs]] [-list] <root>
//
// Without -step: fail on a parse error or a scenario without a level tag
// (@unit or @integration). With -step N: also fail if any @step-N feature is
// still @draft (the review gate), if no feature carries @step-N, or if a
// features/ folder has no TestFeatures suite next to it (go test would then
// pass with zero scenarios run: a false green).
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	gherkin "github.com/cucumber/gherkin/go/v26"
	messages "github.com/cucumber/messages/go/v21"
)

type featureInfo struct {
	path      string
	draft     bool
	steps     map[string]bool
	scenarios int
}

func main() {
	step := flag.String("step", "", "build step number to gate on")
	list := flag.Bool("list", false, "print one line per feature")
	dirs := flag.Bool("dirs", false, "with -step: print only the package dirs that run that step's features")
	flag.Parse()
	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}

	var files []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(p, ".feature") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)

	problems := 0
	var infos []featureInfo
	for _, p := range files {
		info, errs := check(p)
		for _, e := range errs {
			fmt.Println("FAIL", e)
		}
		problems += len(errs)
		infos = append(infos, info)
	}

	total := 0
	for _, in := range infos {
		total += in.scenarios
		if *list {
			state := "approved"
			if in.draft {
				state = "@draft"
			}
			shown := in.path
			if rel, err := filepath.Rel(root, in.path); err == nil {
				shown = rel
			}
			fmt.Printf("%-9s %-62s %3d scenarios\n", state, shown, in.scenarios)
		}
	}

	if *step != "" {
		tag := "@step-" + *step
		found := 0
		pkgDirs := map[string]bool{}
		for _, in := range infos {
			if !in.steps[tag] {
				continue
			}
			found++
			if in.draft {
				fmt.Printf("GATE %s is still @draft: get it reviewed before implementing\n", in.path)
				problems++
			}
			pkgDir := suiteDir(in.path)
			pkgDirs[pkgDir] = true
			if !hasSuite(pkgDir) {
				fmt.Printf("FAIL %s has no TestFeatures suite in %s (go test would run 0 scenarios)\n", in.path, pkgDir)
				problems++
			}
		}
		if *dirs {
			if problems > 0 || found == 0 {
				os.Exit(1)
			}
			var out []string
			for d := range pkgDirs {
				out = append(out, d)
			}
			sort.Strings(out)
			fmt.Println(strings.Join(out, "\n"))
			return
		}
		if found == 0 {
			fmt.Printf("FAIL no feature is tagged %s\n", tag)
			problems++
		}
	}

	fmt.Printf("speccheck: %d files, %d scenarios, %d problems\n", len(files), total, problems)
	if problems > 0 {
		os.Exit(1)
	}
}

func check(path string) (featureInfo, []string) {
	info := featureInfo{path: path, steps: map[string]bool{}}
	f, err := os.Open(path)
	if err != nil {
		return info, []string{err.Error()}
	}
	defer f.Close()

	newID := (&messages.UUID{}).NewId
	doc, err := gherkin.ParseGherkinDocument(f, newID)
	if err != nil {
		return info, []string{fmt.Sprintf("%s: %v", path, err)}
	}
	if doc.Feature == nil {
		return info, []string{path + ": no Feature"}
	}
	for _, t := range doc.Feature.Tags {
		if t.Name == "@draft" {
			info.draft = true
		}
		if strings.HasPrefix(t.Name, "@step-") {
			info.steps[t.Name] = true
		}
	}

	var errs []string
	for _, pk := range gherkin.Pickles(*doc, path, newID) {
		info.scenarios++
		// Feature and scenario tags are merged into the pickle; count distinct.
		levels := map[string]bool{}
		for _, t := range pk.Tags {
			if t.Name == "@unit" || t.Name == "@integration" {
				levels[t.Name] = true
			}
		}
		switch len(levels) {
		case 0:
			errs = append(errs, fmt.Sprintf("%s: %q has no level tag (@unit or @integration)", path, pk.Name))
		case 2:
			errs = append(errs, fmt.Sprintf("%s: %q has both @unit and @integration: pick one", path, pk.Name))
		}
	}
	return info, errs
}

// suiteDir is where a feature's TestFeatures suite lives: the feature's own
// folder (steps next to specs), else the folder above a "features" folder.
func suiteDir(featurePath string) string {
	own := filepath.Dir(featurePath)
	if hasSuite(own) {
		return own
	}
	return filepath.Dir(own)
}

// hasSuite reports whether dir has a _test.go file defining TestFeatures.
func hasSuite(dir string) bool {
	matches, _ := filepath.Glob(filepath.Join(dir, "*_test.go"))
	for _, m := range matches {
		b, err := os.ReadFile(m)
		if err == nil && strings.Contains(string(b), "func TestFeatures(") {
			return true
		}
	}
	return false
}
