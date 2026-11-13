package app

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Add, edit and remove owned jobs (docs/crons.md, "Add, edit and remove owned
// jobs"): the verbs `tackroom cron add|edit|remove` and the view's POST
// /api/crons/jobs share changeCronJob. It edits crons.jobs.<id> in the YAML
// file that declares the job, then runs `cron apply`, the only writer of
// native units.

// cronNotOwnedNote ends every refusal for an id that is not an owned job.
const cronNotOwnedNote = "tackroom creates, changes and deletes only the jobs it owns (dev.tackroom.<id>, tackroom-<id>.timer, # tackroom:<id>); any other launchd, systemd, crontab or other job is never touched by these commands"

// cronJobChange is one add, edit or remove, and the view's request body.
// Nil fields are left as they are on an edit.
type cronJobChange struct {
	Action   string   `json:"action"` // add, edit or remove
	ID       string   `json:"id"`
	Schedule *string  `json:"schedule"`
	Command  []string `json:"command"` // the executable and its arguments
	Host     *string  `json:"host"`    // on an edit, "" removes the field
	Effect   *string  `json:"effect"`  // on an edit, "" removes the field
	Local    bool     `json:"local"`   // add: write tackroom.local.yaml, not tackroom.yaml
	DryRun   bool     `json:"dry_run"`
	NoApply  bool     `json:"no_apply"`
	// LocalOnly refuses a job for another host. The view sets it; a request
	// cannot.
	LocalOnly bool `json:"-"`
}

type cronJobChangeResult struct {
	Action string   `json:"action"`
	ID     string   `json:"id"`
	DryRun bool     `json:"dry_run"`
	Files  []string `json:"files"` // YAML files written, or that a dry run would write
	Diff   string   `json:"diff"`  // the YAML change, changed lines with two lines of context
	Plan   []string `json:"plan"`  // the lines `cron apply` printed
	// Job is the job's record from a fresh scan of this machine, for the view
	// to refresh one row; nil after a removal or a dry run.
	Job *cronJob `json:"job"`
}

// cronFileEdit is a config file before and after a change; equal when a
// change leaves the file alone.
type cronFileEdit struct {
	path          string
	before, after []byte
}

// changeCronJob applies ch to the config at sharedPath. A change that is
// refused is refused before anything is written.
func changeCronJob(sharedPath string, ch cronJobChange) (cronJobChangeResult, error) {
	res := cronJobChangeResult{Action: ch.Action, ID: ch.ID, DryRun: ch.DryRun, Files: []string{}, Plan: []string{}}
	home, err := os.UserHomeDir()
	if err != nil {
		return res, err
	}
	shared, err := resolveConfigPath(sharedPath, home)
	if err != nil {
		return res, err
	}
	if _, err := os.Stat(shared); err != nil {
		return res, fmt.Errorf("cron %s needs a config: %w", ch.Action, err)
	}
	// The edit holds the cron apply lock, so two edits never interleave, and
	// lets go before `cron apply` takes it. A dry run writes nothing.
	unlock := func() {}
	if !ch.DryRun {
		if unlock, err = lockCronApply(home); err != nil {
			return res, err
		}
	}
	defer unlock()
	manifest, err := loadCronsManifest(shared)
	if err != nil {
		return res, err
	}
	edits, err := planCronJobChange(shared, home, manifest, ch)
	if err != nil {
		return res, err
	}
	for _, edit := range edits {
		if !bytes.Equal(edit.before, edit.after) {
			res.Files = append(res.Files, edit.path)
			res.Diff += trimDiffContext(unifiedConfigDiff(edit.path, edit.before, edit.after))
		}
	}
	if ch.DryRun {
		if ch.NoApply {
			return res, nil
		}
		out, err := previewCronApply(shared, edits)
		if err != nil {
			return res, fmt.Errorf("cron apply would fail: %w", err)
		}
		res.Plan = planLines(out)
		return res, nil
	}
	for _, edit := range edits {
		if bytes.Equal(edit.before, edit.after) {
			continue
		}
		mode := os.FileMode(0o644)
		if info, err := os.Stat(edit.path); err == nil {
			mode = info.Mode().Perm()
		}
		if err := atomicConfigWrite(edit.path, edit.after, mode); err != nil {
			return res, err
		}
	}
	unlock()
	if ch.NoApply {
		return res, nil
	}
	out, err := runCronApplyCommand(shared, false)
	if err != nil {
		saved := make([]string, len(res.Files))
		for i, path := range res.Files {
			saved[i] = displayPath(path, home)
		}
		return res, fmt.Errorf("saved %s, but cron apply failed: %w", strings.Join(saved, " and "), err)
	}
	res.Plan = planLines(out)
	return res, nil
}

// planCronJobChange validates ch against the manifest and returns the two
// config files (tackroom.local.yaml, then the shared one) as they would be.
func planCronJobChange(shared, home string, manifest cronsManifest, ch cronJobChange) ([]cronFileEdit, error) {
	id := ch.ID
	if ch.Local && ch.Action != "add" {
		return nil, errors.New("local applies to add only; an edit or removal changes the file that declares the job")
	}
	if ch.Action == "remove" && (ch.Schedule != nil || ch.Command != nil || ch.Host != nil || ch.Effect != nil) {
		return nil, errors.New("remove takes no fields")
	}
	for _, field := range []**string{&ch.Schedule, &ch.Host, &ch.Effect} {
		if *field != nil {
			trimmed := strings.TrimSpace(**field)
			*field = &trimmed
		}
	}
	type parsed struct {
		path       string
		data       []byte
		root, job  *yaml.Node
		changeable bool
	}
	var files []*parsed
	var declared *parsed // the file whose entry wins, as the manifest merge decides: local first
	for _, path := range []string{filepath.Join(filepath.Dir(shared), "tackroom.local.yaml"), shared} {
		data, root, job, err := cronJobNode(path, id)
		if err != nil {
			return nil, err
		}
		file := &parsed{path: path, data: data, root: root, job: job}
		files = append(files, file)
		if job != nil && declared == nil {
			declared = file
		}
	}

	var entry cronManifestJob
	switch ch.Action {
	case "add":
		if !ownedCronIDPattern.MatchString(id) {
			return nil, fmt.Errorf("invalid job id %q: owned job ids match %s", id, ownedCronIDPattern)
		}
		if builtinCronIDs[id] {
			return nil, fmt.Errorf("job id %q is reserved for `tackroom cron`", id)
		}
		if declared != nil {
			return nil, fmt.Errorf("crons.jobs.%s is already declared in %s; change it with `tackroom cron edit`, or by hand when it is an inventory annotation", id, displayPath(declared.path, home))
		}
		if ch.Schedule == nil || ch.Command == nil {
			return nil, errors.New("add needs a schedule and a command")
		}
		target := files[1] // the shared file
		if ch.Local {
			target = files[0]
		}
		if target.root == nil || target.root.Kind == 0 {
			target.root = &yaml.Node{Kind: yaml.DocumentNode} // no file yet, or an empty one
		}
		root, err := yamlRootMapping(target.root)
		if err != nil {
			return nil, err
		}
		target.job = ensureMappingValue(ensureMappingValue(ensureMappingValue(root, "crons"), "jobs"), id)
		target.changeable = true
	case "edit", "remove":
		if declared == nil {
			if !ownedCronIDPattern.MatchString(id) {
				return nil, fmt.Errorf("%q is not a valid owned job id (%s); %s", id, ownedCronIDPattern, cronNotOwnedNote)
			}
			return nil, fmt.Errorf("crons.jobs.%s is not declared in %s or tackroom.local.yaml; %s", id, filepath.Base(shared), cronNotOwnedNote)
		}
		if err := declared.job.Decode(&entry); err != nil {
			return nil, fmt.Errorf("parse crons.jobs.%s in %s: %w", id, declared.path, err)
		}
		if entry.Schedule == "" && len(entry.Command) == 0 {
			return nil, fmt.Errorf("crons.jobs.%s in %s has no schedule and command: it is an inventory annotation, not an owned job; edit it by hand; %s", id, displayPath(declared.path, home), cronNotOwnedNote)
		}
		if ch.LocalOnly {
			if err := checkCronHost(manifest, entry.Host, true); err != nil {
				return nil, err
			}
		}
		if ch.Action == "edit" && ch.Schedule == nil && ch.Command == nil && ch.Host == nil && ch.Effect == nil {
			return nil, errors.New("nothing to change: pass a schedule, command, host or effect")
		}
		for _, file := range files {
			file.changeable = file.job != nil && (ch.Action == "remove" || file == declared)
		}
	default:
		return nil, fmt.Errorf("unknown action %q", ch.Action)
	}

	host := entry.Host
	if ch.Host != nil {
		host = *ch.Host
		if err := checkCronHost(manifest, host, ch.LocalOnly); err != nil {
			return nil, err
		}
	}
	if ch.Schedule != nil {
		if _, err := cronToCalendarIntervals(*ch.Schedule); err != nil {
			return nil, err
		}
	}
	if ch.Command != nil {
		if len(ch.Command) == 0 || strings.TrimSpace(ch.Command[0]) == "" {
			return nil, errors.New("command cannot be empty")
		}
		// An unresolvable command would make every later `cron apply` stop, so
		// refuse it while this machine is the one that will install it.
		if !ch.NoApply && (host == "" || host == manifest.Self) {
			if _, err := resolveCronCommand(ch.Command, home); err != nil {
				return nil, err
			}
		}
	}

	edits := make([]cronFileEdit, len(files))
	for i, file := range files {
		edits[i] = cronFileEdit{path: file.path, before: file.data, after: file.data}
		if !file.changeable {
			continue
		}
		var job *yaml.Node // nil removes the entry
		if ch.Action == "remove" {
			jobs := mappingValue(mappingValue(rootMapping(file.root), "crons"), "jobs")
			at := mappingIndex(jobs, id)
			jobs.Content = append(jobs.Content[:at], jobs.Content[at+2:]...)
		} else {
			if err := setCronJobFields(file.job, ch, id, file.path); err != nil {
				return nil, err
			}
			job = file.job
		}
		indent := yamlIndentOf(file.data)
		after, ok := spliceCronJob(file.data, id, job, indent)
		if !ok {
			var err error
			if after, err = marshalSettingsYAML(file.root, indent); err != nil {
				return nil, err
			}
		}
		edits[i].after = after
	}
	return edits, nil
}

// annotateCronEditable marks the jobs `cron edit` and `cron remove` can change
// from this machine, and gives the reason for each one they cannot, for the
// clients to show. changeCronJob makes the same decisions again when it runs.
func annotateCronEditable(jobs []cronJob, manifest cronsManifest) {
	for i := range jobs {
		job := &jobs[i]
		entry := manifest.Jobs[job.ID]
		declared := entry.Schedule != "" && len(entry.Command) > 0
		switch {
		case job.State == "missing" && !declared:
			job.EditNote = "Declared in crons.jobs as a note, not as a job tackroom owns. Edit it by hand."
		case job.State != "missing" && !job.Owned:
			job.EditNote = "tackroom changes only the jobs it installs from crons.jobs (dev.tackroom.<id>). This one lives outside it; change it where it lives."
		case job.State != "missing" && job.Host != "local":
			job.EditNote = fmt.Sprintf("This job runs on %s. Edit tackroom.yaml and let that machine run cron apply.", job.Host)
		case !declared:
			job.EditNote = "No longer declared in crons.jobs. Run tackroom cron apply to remove its unit."
		case entry.Host != "" && entry.Host != manifest.Self:
			job.EditNote = fmt.Sprintf("Declared for host %s, not this machine. Edit tackroom.yaml and let that machine run cron apply.", entry.Host)
		default:
			job.Editable = true
		}
	}
}

// checkCronHost refuses a host `cron apply` would stop on (crons.self unset)
// and, for the view, a job that belongs to another machine.
func checkCronHost(manifest cronsManifest, host string, localOnly bool) error {
	switch {
	case host == "":
		return nil
	case manifest.Self == "":
		return fmt.Errorf("host %q needs crons.self in tackroom.local.yaml to name this machine; nothing was changed", host)
	case localOnly && host != manifest.Self:
		return fmt.Errorf("this job is for host %q, not this machine (%q): edit tackroom.yaml and let that machine run cron apply", host, manifest.Self)
	}
	return nil
}

// setCronJobFields writes the fields ch carries into the job mapping. Existing
// values keep their comments and position; new keys go at the end.
func setCronJobFields(job *yaml.Node, ch cronJobChange, id, path string) error {
	setScalar := func(key string, value *string, style yaml.Style) error {
		if value == nil {
			return nil
		}
		i := mappingIndex(job, key)
		switch {
		case *value == "":
			if i >= 0 {
				job.Content = append(job.Content[:i], job.Content[i+2:]...)
			}
		case i < 0:
			job.Content = append(job.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: *value, Style: style})
		case job.Content[i+1].Kind != yaml.ScalarNode:
			return fmt.Errorf("crons.jobs.%s.%s in %s is not a string", id, key, path)
		default:
			node := job.Content[i+1]
			node.Value, node.Tag = *value, "!!str"
			if style != 0 {
				node.Style = style
			}
		}
		return nil
	}
	if err := setScalar("host", ch.Host, 0); err != nil {
		return err
	}
	if err := setScalar("schedule", ch.Schedule, yaml.DoubleQuotedStyle); err != nil {
		return err
	}
	if ch.Command != nil {
		items := make([]*yaml.Node, len(ch.Command))
		for i, arg := range ch.Command {
			items[i] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: arg}
		}
		if i := mappingIndex(job, "command"); i >= 0 && job.Content[i+1].Kind == yaml.SequenceNode {
			job.Content[i+1].Content = items
		} else {
			setMappingNode(job, "command", &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle, Content: items})
		}
	}
	return setScalar("effect", ch.Effect, 0)
}

// spliceCronJob returns data with crons.jobs.<id> set to job, or removed when
// job is nil. Only that entry's own lines change, so every other byte stays as
// the user wrote it. ok is false when the layout cannot be edited that way
// (flow style, no block mapping to extend, an empty file, a result that does
// not read back as intended); the caller then re-encodes the whole document.
func spliceCronJob(data []byte, id string, job *yaml.Node, indent int) (out []byte, ok bool) {
	var doc yaml.Node
	if len(data) == 0 || yaml.Unmarshal(data, &doc) != nil || len(doc.Content) == 0 {
		return nil, false
	}
	// stack holds the block mappings from the document root down to the
	// deepest of crons and jobs that exists; keys[l] is the index in stack[l]
	// of the key that leads to stack[l+1].
	path := []string{"crons", "jobs"}
	stack, keys := []*yaml.Node{doc.Content[0]}, []int{}
	for {
		m := stack[len(stack)-1]
		if m.Kind != yaml.MappingNode || m.Style&yaml.FlowStyle != 0 || len(m.Content) == 0 {
			return nil, false
		}
		if len(stack) > len(path) {
			break
		}
		i := mappingIndex(m, path[len(stack)-1])
		if i < 0 {
			break
		}
		keys = append(keys, i)
		stack = append(stack, m.Content[i+1])
	}
	depth := len(stack) - 1

	lines := strings.SplitAfter(string(data), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	commentOnly := func(line string) bool {
		t := strings.TrimSpace(line)
		return t == "" || strings.HasPrefix(t, "#")
	}
	// endOf is the last line of the entry at stack[level].Content[i]: the line
	// before the next key at any level, less the blank and comment lines that
	// trail it (they belong to what follows). next is where that key starts.
	endOf := func(level, i int) (end, next int) {
		start := stack[level].Content[i].Line
		next = len(lines) + 1
		for ; level >= 0; level-- {
			if i+2 < len(stack[level].Content) {
				next = stack[level].Content[i+2].Line
				break
			}
			if level > 0 {
				i = keys[level-1]
			}
		}
		end = next - 1
		for end > start && commentOnly(lines[end-1]) {
			end--
		}
		return end, next
	}
	encode := func(wrap []string, key *yaml.Node, column int) ([]string, bool) {
		entry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{key, job}}
		for i := len(wrap) - 1; i >= 0; i-- {
			entry = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: wrap[i]}, entry}}
		}
		text, err := marshalSettingsYAML(entry, indent)
		if err != nil {
			return nil, false
		}
		encoded := strings.SplitAfter(strings.TrimSuffix(string(text), "\n"), "\n")
		encoded[len(encoded)-1] += "\n"
		for len(encoded) > 1 && commentOnly(encoded[len(encoded)-1]) {
			encoded = encoded[:len(encoded)-1] // a foot comment stays where it is in the file
		}
		for i := range encoded {
			encoded[i] = strings.Repeat(" ", column-1) + encoded[i]
		}
		return encoded, true
	}

	var first, last int // lines first..last are replaced; last == first-1 inserts
	var text []string
	ok = true
	at := -1
	if depth == len(path) {
		at = mappingIndex(stack[depth], id)
	}
	switch {
	case at >= 0:
		var next int
		first = stack[depth].Content[at].Line
		last, next = endOf(depth, at)
		if job == nil {
			for first > 1 && strings.HasPrefix(strings.TrimSpace(lines[first-2]), "#") {
				first-- // the comment lines right above the entry go with it
			}
			// The last job takes with it a jobs: and crons: block that holds
			// nothing else, the way `cron add` made them. A comment anywhere
			// in the block, or any other key, keeps the block.
			blank := func(from, to int) bool { // lines strictly between from and to
				for n := from + 1; n < to; n++ {
					if strings.TrimSpace(lines[n-1]) != "" {
						return false
					}
				}
				return true
			}
			bare := func(key *yaml.Node) bool { return key.HeadComment+key.LineComment+key.FootComment == "" }
			jobsKey := stack[1].Content[keys[1]]
			if len(stack[depth].Content) == 2 && bare(jobsKey) && blank(jobsKey.Line, first) && blank(last, next) {
				first = jobsKey.Line
				cronsKey := stack[0].Content[keys[0]]
				if len(stack[1].Content) == 2 && len(stack[0].Content) > 2 && bare(cronsKey) && blank(cronsKey.Line, jobsKey.Line) {
					first = cronsKey.Line
				}
			}
		} else {
			key := *stack[depth].Content[at]
			key.HeadComment, key.FootComment = "", ""
			text, ok = encode(nil, &key, key.Column)
		}
	case job != nil:
		last, _ = endOf(depth, len(stack[depth].Content)-2)
		first = last + 1
		text, ok = encode(path[depth:], &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: id}, stack[depth].Content[0].Column)
		if ok && !strings.HasSuffix(lines[last-1], "\n") {
			lines[last-1] += "\n"
		}
	default:
		return nil, false
	}
	if !ok {
		return nil, false
	}
	spliced := append(append(append([]string{}, lines[:first-1]...), text...), lines[last:]...)
	out = []byte(strings.Join(spliced, ""))

	// Read the result back: the entry must be what was asked for.
	var check struct {
		Crons cronsManifest `yaml:"crons"`
	}
	if yaml.Unmarshal(out, &check) != nil {
		return nil, false
	}
	got, present := check.Crons.Jobs[id]
	if present != (job != nil) {
		return nil, false
	}
	if job != nil {
		var want cronManifestJob
		if job.Decode(&want) != nil || !reflect.DeepEqual(got, want) {
			return nil, false
		}
	}
	return out, true
}

// cronJobNode parses path and returns its bytes, its document and the mapping
// at crons.jobs.<id>. The job is nil when the entry is absent; all three are
// nil when the file is.
func cronJobNode(path, id string) ([]byte, *yaml.Node, *yaml.Node, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, nil, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(root.Content) == 0 {
		return data, &root, nil, nil
	}
	node := root.Content[0]
	for _, key := range []string{"crons", "jobs", id} {
		if node = dshMappingValue(node, key); node == nil {
			return data, &root, nil, nil
		}
	}
	if node.Kind != yaml.MappingNode {
		return nil, nil, nil, fmt.Errorf("crons.jobs.%s in %s is not a mapping", id, path)
	}
	return data, &root, node, nil
}

// trimDiffContext cuts a whole-file diff down to the changed lines and two
// lines of context around each.
func trimDiffContext(diff string) string {
	lines := strings.Split(strings.TrimSuffix(diff, "\n"), "\n")
	if len(lines) <= 2 {
		return diff
	}
	keep := make([]bool, len(lines))
	for i := 2; i < len(lines); i++ {
		if lines[i][0] == '+' || lines[i][0] == '-' {
			for j := max(i-2, 2); j <= min(i+2, len(lines)-1); j++ {
				keep[j] = true
			}
		}
	}
	out := lines[0] + "\n" + lines[1] + "\n"
	for i := 2; i < len(lines); i++ {
		if keep[i] {
			if i > 2 && !keep[i-1] {
				out += "@@\n"
			}
			out += lines[i] + "\n"
		}
	}
	return out
}

func planLines(out string) []string {
	if out == "" {
		return []string{}
	}
	return strings.Split(out, "\n")
}

// runCronApplyCommand runs `cron apply` as a child process and returns what it
// printed, so the plan reaches the view and the CLI the same way.
func runCronApplyCommand(configPath string, dryRun bool) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	args := []string{"cron", "apply", "--config", configPath}
	if dryRun {
		args = append(args, "--dry-run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, args...).CombinedOutput()
	if err != nil {
		return "", errors.New(commandFailure(out, err))
	}
	return strings.TrimSpace(string(out)), nil
}

// previewCronApply prints the plan `cron apply` would follow for the edited
// files without writing them: it plans from copies of their crons: sections in
// a private temporary directory.
func previewCronApply(shared string, edits []cronFileEdit) (string, error) {
	dir, err := os.MkdirTemp("", "tackroom-cron-preview-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	for _, edit := range edits {
		var doc struct {
			Crons yaml.Node `yaml:"crons"`
		}
		if err := yaml.Unmarshal(edit.after, &doc); err != nil {
			return "", fmt.Errorf("parse %s: %w", edit.path, err)
		}
		data := []byte("{}\n")
		if doc.Crons.Kind != 0 {
			if data, err = yaml.Marshal(map[string]*yaml.Node{"crons": &doc.Crons}); err != nil {
				return "", err
			}
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(edit.path)), data, 0o600); err != nil {
			return "", err
		}
	}
	return runCronApplyCommand(filepath.Join(dir, filepath.Base(shared)), true)
}

// runCronJobCommand is `tackroom cron add|edit|remove ID [flags]`.
func runCronJobCommand(action string, args []string) error {
	fs := flag.NewFlagSet("cron "+action, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "Path to tackroom YAML config")
	dryRun := fs.Bool("dry-run", false, "Print the YAML change and the apply plan, write nothing")
	noApply := fs.Bool("no-apply", false, "Change the YAML only; run tackroom cron apply later")
	var schedule, command, host, effect string
	var commandArgs stringListFlag
	var local bool
	if action != "remove" {
		fs.StringVar(&schedule, "schedule", "", `5-field cron expression "MIN HOUR DOM MON DOW"`)
		fs.StringVar(&command, "command", "", "Executable to run: a name on PATH or an absolute path. The job's command is stored as a YAML list of this and every --arg")
		fs.Var(&commandArgs, "arg", "Argument for the command, one per --arg (repeatable, in order)")
		fs.StringVar(&host, "host", "", "Machine the job runs on, matched against crons.self; omit to run on every machine")
		fs.StringVar(&effect, "effect", "", "What the job changes, shown in the Crons tab")
	}
	if action == "add" {
		fs.BoolVar(&local, "local", false, "Write tackroom.local.yaml instead of tackroom.yaml")
	}
	// The id may come before, between or after the flags: Go's flag package
	// stops at the first plain word, so parse again after each one.
	var words []string
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			break
		}
		words, rest = append(words, fs.Arg(0)), fs.Args()[1:]
	}
	if len(words) > 1 {
		return fmt.Errorf("usage: tackroom cron %s ID [flags]; the command is --command plus one --arg per argument", action)
	}
	id := ""
	if len(words) == 1 {
		id = words[0]
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	if id == "" {
		return fmt.Errorf("cron %s needs a job id", action)
	}
	ch := cronJobChange{Action: action, ID: id, Local: local, DryRun: *dryRun, NoApply: *noApply}
	if action != "remove" {
		if given["arg"] && !given["command"] {
			return errors.New("--arg needs --command: a command is replaced as a whole")
		}
		if given["schedule"] || action == "add" {
			ch.Schedule = &schedule
		}
		if given["command"] || action == "add" {
			ch.Command = []string{}
			if command != "" {
				ch.Command = append([]string{command}, commandArgs...)
			}
		}
		if given["host"] {
			ch.Host = &host
		}
		if given["effect"] {
			ch.Effect = &effect
		}
	}
	res, err := changeCronJob(*configPath, ch)
	if err != nil && len(res.Files) == 0 && len(res.Plan) == 0 {
		return err
	}
	if res.DryRun {
		fmt.Println("dry run: nothing written")
	}
	fmt.Print(res.Diff)
	for _, line := range res.Plan {
		fmt.Println(line)
	}
	return err
}

// handleCronJobs is POST /api/crons/jobs: add, edit or remove an owned job on
// this machine. The body is a cronJobChange; the answer is a
// cronJobChangeResult. The declaration in the YAML decides, not the last scan,
// and a job for another host is refused.
func (s *configWebServer) handleCronJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !s.authorizeAPI(w, r, true) {
		return
	}
	var ch cronJobChange
	if err := decodeJSONBody(r, &ch); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if ch.Action != "add" && ch.Action != "edit" && ch.Action != "remove" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "action must be add, edit or remove")
		return
	}
	ch.LocalOnly = true
	cronActionMu.Lock()
	res, err := changeCronJob(s.doc.sharedPath, ch)
	if err == nil && !ch.DryRun {
		if scan, scanErr := collectCrons(s.doc.sharedPath, []string{"local"}); scanErr == nil {
			for i := range scan.Jobs {
				if scan.Jobs[i].ID == ch.ID {
					res.Job = &scan.Jobs[i]
					break
				}
			}
		}
	}
	cronActionMu.Unlock()
	if !ch.DryRun {
		if err == nil {
			patchCronScan(func(job cronJob) bool { return job.ID == ch.ID && (job.Host == "local" || job.State == "missing") }, res.Job)
		} else {
			invalidateCronScan() // apply may have changed units before it failed
		}
	}
	if err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "cron_action_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}
