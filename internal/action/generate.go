package action

import (
	"crypto/rand"
	"fmt"
	"path"
	"strings"

	"github.com/dotfrankruan/visualible/internal/ir"
)

func mustRand(b []byte) {
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
}

func became() *bool { t := true; return &t }

func truncate(s string, n int) string {
	s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// =====================================================================
// Install software (optionally starting/enabling its service) — compound
// =====================================================================

type installSoftware struct{}

func (installSoftware) Definition() Definition {
	return Definition{
		ID:      "install-software",
		Name:    "Install software",
		Icon:    "📦",
		Summary: "Install, update or remove a package",
		Explanation: "This makes sure the package is installed. Running the automation again " +
			"will not reinstall it if it is already there.",
		Fields: []Field{
			{ID: "package", Label: "Package", Type: "text", Required: true, Placeholder: "nginx"},
			{ID: "state", Label: "State", Type: "select", Default: "present", Choices: []Choice{
				{Value: "present", Label: "Installed"},
				{Value: "latest", Label: "Up to date (latest version)"},
				{Value: "absent", Label: "Removed"},
			}},
			{ID: "serviceName", Label: "Service name", Type: "text", Placeholder: "nginx",
				Help: "Optional. If this package installs a service, Visualible can start it for you."},
			{ID: "ensureRunning", Label: "Make sure the service is running", Type: "bool", Default: false},
			{ID: "enableBoot", Label: "Start automatically at boot", Type: "bool", Default: false},
		},
	}
}

func (installSoftware) Generate(params map[string]any) (*Generated, error) {
	pkg := strings.TrimSpace(str(params, "package"))
	if pkg == "" {
		return nil, fmt.Errorf("Package name is required.")
	}
	state := str(params, "state")
	if state == "" {
		state = "present"
	}
	verb := "Install"
	noun := "Install"
	switch state {
	case "absent":
		verb, noun = "Remove", "Remove"
	case "latest":
		verb, noun = "Update", "Keep up to date"
	}
	pkgTask := &ir.Task{
		ID:     newID("t"),
		Name:   fmt.Sprintf("%s %s", noun, pkg),
		Module: "ansible.builtin.package",
		Args:   map[string]any{"name": pkg, "state": state},
		Become: became(),
	}
	gen := &Generated{Tasks: []*ir.Task{pkgTask}}

	service := strings.TrimSpace(str(params, "serviceName"))
	ensure := boolParam(params, "ensureRunning")
	boot := boolParam(params, "enableBoot")
	if service != "" && (ensure || boot) {
		args := map[string]any{"name": service}
		if ensure {
			args["state"] = "started"
		}
		if boot {
			args["enabled"] = true
		}
		name := fmt.Sprintf("Start %s", service)
		if !ensure {
			name = fmt.Sprintf("Enable %s at boot", service)
		}
		gen.Tasks = append(gen.Tasks, &ir.Task{
			ID:     newID("t"),
			Name:   name,
			Module: "ansible.builtin.service",
			Args:   args,
			Become: became(),
		})
	} else if service != "" {
		return nil, fmt.Errorf("Choose what to do with the %q service: make it run, start it at boot, or both.", service)
	}
	_ = verb
	return gen, nil
}

func (installSoftware) Recognize(t *ir.Task) (map[string]any, bool) {
	args, ok := match(t,
		[]string{"ansible.builtin.package", "ansible.builtin.apt", "ansible.builtin.dnf", "ansible.builtin.yum"},
		[]string{"name", "state"}, false)
	if !ok {
		return nil, false
	}
	pkg := singleName(args["name"])
	if pkg == "" {
		return nil, false
	}
	state, _ := args["state"].(string)
	if state == "" {
		state = "present"
	}
	switch state {
	case "present", "latest", "absent", "installed", "removed":
	default:
		return nil, false
	}
	if state == "installed" {
		state = "present"
	}
	if state == "removed" {
		state = "absent"
	}
	return map[string]any{"package": pkg, "state": state}, true
}

func (installSoftware) Describe(params map[string]any) (string, string) {
	pkg := str(params, "package")
	state := str(params, "state")
	label := fmt.Sprintf("Install %s", pkg)
	switch state {
	case "absent":
		label = fmt.Sprintf("Remove %s", pkg)
	case "latest":
		label = fmt.Sprintf("Update %s", pkg)
	}
	return label, fmt.Sprintf("Package: %s · %s", pkg, stateLabel("", state))
}

// singleName accepts a string or a single-element list (Ansible allows
// both for package names); multi-element lists are not representable in
// the curated single-package form.
func singleName(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		if len(x) == 1 {
			if s, ok := x[0].(string); ok {
				return s
			}
		}
	case []string:
		if len(x) == 1 {
			return x[0]
		}
	}
	return ""
}

// =====================================================================
// Manage a service
// =====================================================================

type manageService struct{}

func (manageService) Definition() Definition {
	return Definition{
		ID:      "manage-service",
		Name:    "Manage a service",
		Icon:    "⚙",
		Summary: "Start, stop or restart a service",
		Explanation: "This controls a system service. Starting an already-running service " +
			"does nothing, so it is safe to run again.",
		Fields: []Field{
			{ID: "service", Label: "Service", Type: "text", Required: true, Placeholder: "nginx"},
			{ID: "state", Label: "State", Type: "select", Default: "started", Choices: []Choice{
				{Value: "started", Label: "Running"},
				{Value: "restarted", Label: "Restarted"},
				{Value: "reloaded", Label: "Reloaded (pick up config changes)"},
				{Value: "stopped", Label: "Stopped"},
			}},
			{ID: "enabledAtBoot", Label: "Start automatically at boot", Type: "bool", Default: false},
		},
	}
}

func (manageService) Generate(params map[string]any) (*Generated, error) {
	svc := strings.TrimSpace(str(params, "service"))
	if svc == "" {
		return nil, fmt.Errorf("Service name is required.")
	}
	state := str(params, "state")
	if state == "" {
		state = "started"
	}
	args := map[string]any{"name": svc}
	if state != "" {
		args["state"] = state
	}
	if boolParam(params, "enabledAtBoot") {
		args["enabled"] = true
	}
	verb := map[string]string{
		"started": "Start", "stopped": "Stop", "restarted": "Restart", "reloaded": "Reload",
	}[state]
	if verb == "" {
		verb = "Manage"
	}
	name := fmt.Sprintf("%s %s", verb, svc)
	if boolParam(params, "enabledAtBoot") && state == "started" {
		name = fmt.Sprintf("Start %s and enable it at boot", svc)
	}
	return &Generated{Tasks: []*ir.Task{{
		ID:     newID("t"),
		Name:   name,
		Module: "ansible.builtin.service",
		Args:   args,
		Become: became(),
	}}}, nil
}

func (manageService) Recognize(t *ir.Task) (map[string]any, bool) {
	// notify is allowed: it does not change what the service step does, and
	// the curated editor preserves an existing notify when regenerating.
	args, ok := match(t,
		[]string{"ansible.builtin.service", "ansible.builtin.systemd_service", "ansible.builtin.systemd"},
		[]string{"name", "state", "enabled"}, true)
	if !ok {
		return nil, false
	}
	svc, _ := args["name"].(string)
	if svc == "" {
		return nil, false
	}
	state, _ := args["state"].(string)
	if state == "" {
		// enabled-only task is still representable.
		if enabled, _ := args["enabled"].(bool); enabled {
			return map[string]any{"service": svc, "enabledAtBoot": true}, true
		}
		return nil, false
	}
	switch state {
	case "started", "stopped", "restarted", "reloaded":
	default:
		return nil, false
	}
	params := map[string]any{"service": svc, "state": state}
	if enabled, _ := args["enabled"].(bool); enabled {
		params["enabledAtBoot"] = true
	}
	return params, true
}

func (manageService) Describe(params map[string]any) (string, string) {
	svc := str(params, "service")
	state := str(params, "state")
	verb := map[string]string{
		"started": "Start", "stopped": "Stop", "restarted": "Restart", "reloaded": "Reload",
	}[state]
	if verb == "" {
		verb = "Enable"
	}
	label := fmt.Sprintf("%s %s", verb, svc)
	sub := fmt.Sprintf("Service: %s · %s", svc, stateLabel("", state))
	if boolParam(params, "enabledAtBoot") {
		sub += " · starts at boot"
	}
	return label, sub
}

// =====================================================================
// Create / manage a user (optionally adding an SSH key) — compound
// =====================================================================

type createUser struct{}

func (createUser) Definition() Definition {
	return Definition{
		ID:          "create-user",
		Name:        "Create a user",
		Icon:        "👤",
		Summary:     "Create a user account, optionally with SSH access",
		Explanation: "This creates the account if it is missing. Existing accounts are left alone.",
		Fields: []Field{
			{ID: "username", Label: "Username", Type: "text", Required: true, Placeholder: "frank"},
			{ID: "shell", Label: "Shell", Type: "text", Default: "/bin/bash", Placeholder: "/bin/bash"},
			{ID: "admin", Label: "Administrator privileges", Type: "bool", Default: false,
				Help: "Adds the user to the sudo group (Debian/Ubuntu). Adjust in Advanced details for other systems."},
			{ID: "sshKey", Label: "SSH public key", Type: "textarea",
				Placeholder: "ssh-ed25519 AAAA… user@laptop", Help: "Optional. Paste a public key to allow SSH login."},
		},
	}
}

func (createUser) Generate(params map[string]any) (*Generated, error) {
	user := strings.TrimSpace(str(params, "username"))
	if user == "" {
		return nil, fmt.Errorf("Username is required.")
	}
	args := map[string]any{"name": user}
	if shell := strings.TrimSpace(str(params, "shell")); shell != "" {
		args["shell"] = shell
	}
	if boolParam(params, "admin") {
		args["groups"] = "sudo"
		args["append"] = true
	}
	gen := &Generated{Tasks: []*ir.Task{{
		ID:     newID("t"),
		Name:   fmt.Sprintf("Create user %s", user),
		Module: "ansible.builtin.user",
		Args:   args,
		Become: became(),
	}}}
	if key := strings.TrimSpace(str(params, "sshKey")); key != "" {
		gen.Tasks = append(gen.Tasks, &ir.Task{
			ID:     newID("t"),
			Name:   fmt.Sprintf("Add SSH key for %s", user),
			Module: "ansible.posix.authorized_key",
			Args:   map[string]any{"user": user, "key": key, "state": "present"},
			Become: became(),
		})
	}
	return gen, nil
}

func (createUser) Recognize(t *ir.Task) (map[string]any, bool) {
	args, ok := match(t, []string{"ansible.builtin.user"},
		[]string{"name", "shell", "groups", "append", "state", "create_home"}, false)
	if !ok {
		return nil, false
	}
	user, _ := args["name"].(string)
	if user == "" {
		return nil, false
	}
	state, _ := args["state"].(string)
	if state == "absent" {
		return map[string]any{"username": user, "state": "absent"}, true
	}
	params := map[string]any{"username": user}
	if shell, _ := args["shell"].(string); shell != "" {
		params["shell"] = shell
	}
	groups := argStringList(args, "groups")
	appendFlag, _ := args["append"].(bool)
	for _, g := range groups {
		if (g == "sudo" || g == "wheel") && appendFlag {
			params["admin"] = true
		}
	}
	return params, true
}

func (createUser) Describe(params map[string]any) (string, string) {
	user := str(params, "username")
	if str(params, "state") == "absent" {
		return fmt.Sprintf("Remove user %s", user), "User account"
	}
	sub := "User account"
	if shell := str(params, "shell"); shell != "" {
		sub += " · shell " + shell
	}
	if boolParam(params, "admin") {
		sub += " · administrator"
	}
	return fmt.Sprintf("Create user %s", user), sub
}

// =====================================================================
// Add an SSH key (also produced by Create user)
// =====================================================================

type addSSHKey struct{}

func (addSSHKey) Definition() Definition {
	return Definition{
		ID:          "add-ssh-key",
		Name:        "Add an SSH key",
		Icon:        "🔐",
		Summary:     "Authorize a public key for a user",
		Explanation: "This installs the public key for the account so it can be used to log in.",
		Fields: []Field{
			{ID: "username", Label: "User", Type: "text", Required: true, Placeholder: "frank"},
			{ID: "key", Label: "Public key", Type: "textarea", Required: true,
				Placeholder: "ssh-ed25519 AAAA… user@laptop"},
		},
	}
}

func (addSSHKey) Generate(params map[string]any) (*Generated, error) {
	user := strings.TrimSpace(str(params, "username"))
	key := strings.TrimSpace(str(params, "key"))
	if user == "" || key == "" {
		return nil, fmt.Errorf("A user and a public key are required.")
	}
	return &Generated{Tasks: []*ir.Task{{
		ID:     newID("t"),
		Name:   fmt.Sprintf("Add SSH key for %s", user),
		Module: "ansible.posix.authorized_key",
		Args:   map[string]any{"user": user, "key": key, "state": "present"},
		Become: became(),
	}}}, nil
}

func (addSSHKey) Recognize(t *ir.Task) (map[string]any, bool) {
	args, ok := match(t,
		[]string{"ansible.posix.authorized_key", "ansible.builtin.authorized_key"},
		[]string{"user", "key", "state"}, false)
	if !ok {
		return nil, false
	}
	user, _ := args["user"].(string)
	key, _ := args["key"].(string)
	if user == "" || key == "" {
		return nil, false
	}
	return map[string]any{"username": user, "key": key}, true
}

func (addSSHKey) Describe(params map[string]any) (string, string) {
	return fmt.Sprintf("Add SSH key for %s", str(params, "username")), "Authorized public key"
}

// =====================================================================
// Deploy a file
// =====================================================================

type deployFile struct{}

func (deployFile) Definition() Definition {
	return Definition{
		ID:      "deploy-file",
		Name:    "Deploy a file",
		Icon:    "📄",
		Summary: "Place a file with exact contents on the machines",
		Explanation: "This writes the file with the content you provide. It only reports a change " +
			"when the content actually differs.",
		Fields: []Field{
			{ID: "source", Label: "Source", Type: "select", Default: "content", Choices: []Choice{
				{Value: "content", Label: "Write content directly"},
				{Value: "path", Label: "Copy a file from this machine"},
			}},
			{ID: "content", Label: "Content", Type: "textarea",
				Placeholder: "server {\n  listen 80;\n}", Help: "Used when writing content directly."},
			{ID: "sourcePath", Label: "File on this machine", Type: "text",
				Placeholder: "./nginx.conf", Help: "Used when copying a local file."},
			{ID: "dest", Label: "Destination path", Type: "text", Required: true,
				Placeholder: "/etc/nginx/nginx.conf"},
			{ID: "owner", Label: "Owner", Type: "text", Placeholder: "root"},
			{ID: "group", Label: "Group", Type: "text", Placeholder: "root"},
			{ID: "mode", Label: "Permissions", Type: "text", Default: "0644", Placeholder: "0644"},
		},
	}
}

func (deployFile) Generate(params map[string]any) (*Generated, error) {
	dest := strings.TrimSpace(str(params, "dest"))
	if dest == "" {
		return nil, fmt.Errorf("Destination path is required.")
	}
	args := map[string]any{"dest": dest}
	switch str(params, "source") {
	case "path":
		src := strings.TrimSpace(str(params, "sourcePath"))
		if src == "" {
			return nil, fmt.Errorf("Choose a file to copy.")
		}
		args["src"] = src
	default:
		content := str(params, "content")
		if content == "" {
			return nil, fmt.Errorf("File content is required.")
		}
		args["content"] = content
	}
	if v := strings.TrimSpace(str(params, "owner")); v != "" {
		args["owner"] = v
	}
	if v := strings.TrimSpace(str(params, "group")); v != "" {
		args["group"] = v
	}
	if v := strings.TrimSpace(str(params, "mode")); v != "" {
		args["mode"] = v
	}
	return &Generated{Tasks: []*ir.Task{{
		ID:     newID("t"),
		Name:   fmt.Sprintf("Deploy %s", path.Base(dest)),
		Module: "ansible.builtin.copy",
		Args:   args,
		Become: became(),
	}}}, nil
}

func (deployFile) Recognize(t *ir.Task) (map[string]any, bool) {
	args, ok := match(t, []string{"ansible.builtin.copy"},
		[]string{"content", "src", "dest", "owner", "group", "mode"}, false)
	if !ok {
		return nil, false
	}
	dest, _ := args["dest"].(string)
	if dest == "" {
		return nil, false
	}
	params := map[string]any{"dest": dest}
	if content, ok := args["content"].(string); ok && content != "" {
		params["source"] = "content"
		params["content"] = content
	} else if src, ok := args["src"].(string); ok && src != "" {
		params["source"] = "path"
		params["sourcePath"] = src
	} else {
		return nil, false
	}
	for _, k := range []string{"owner", "group", "mode"} {
		if v, ok := args[k].(string); ok && v != "" {
			params[k] = v
		}
	}
	return params, true
}

func (deployFile) Describe(params map[string]any) (string, string) {
	dest := str(params, "dest")
	return fmt.Sprintf("Deploy %s", path.Base(dest)), dest
}

// =====================================================================
// Render a configuration template (optionally restarting a service)
// =====================================================================

type renderTemplate struct{}

func (renderTemplate) Definition() Definition {
	return Definition{
		ID:      "render-template",
		Name:    "Render a template",
		Icon:    "📝",
		Summary: "Generate a config file from a template with variables",
		Explanation: "Templates are filled in per machine. If you name a service here, it is " +
			"restarted only when the rendered file actually changes.",
		Fields: []Field{
			{ID: "templatePath", Label: "Template file", Type: "text", Required: true,
				Placeholder: "nginx.conf.j2"},
			{ID: "dest", Label: "Destination path", Type: "text", Required: true,
				Placeholder: "/etc/nginx/nginx.conf"},
			{ID: "owner", Label: "Owner", Type: "text", Placeholder: "root"},
			{ID: "group", Label: "Group", Type: "text", Placeholder: "root"},
			{ID: "mode", Label: "Permissions", Type: "text", Default: "0644", Placeholder: "0644"},
			{ID: "restartService", Label: "Restart this service when the file changes", Type: "text",
				Placeholder: "nginx"},
		},
	}
}

func (renderTemplate) Generate(params map[string]any) (*Generated, error) {
	src := strings.TrimSpace(str(params, "templatePath"))
	dest := strings.TrimSpace(str(params, "dest"))
	if src == "" || dest == "" {
		return nil, fmt.Errorf("A template file and a destination path are required.")
	}
	args := map[string]any{"src": src, "dest": dest}
	if v := strings.TrimSpace(str(params, "owner")); v != "" {
		args["owner"] = v
	}
	if v := strings.TrimSpace(str(params, "group")); v != "" {
		args["group"] = v
	}
	if v := strings.TrimSpace(str(params, "mode")); v != "" {
		args["mode"] = v
	}
	task := &ir.Task{
		ID:     newID("t"),
		Name:   fmt.Sprintf("Deploy %s", path.Base(dest)),
		Module: "ansible.builtin.template",
		Args:   args,
		Become: became(),
	}
	gen := &Generated{Tasks: []*ir.Task{task}}
	if svc := strings.TrimSpace(str(params, "restartService")); svc != "" {
		handlerName := fmt.Sprintf("Restart %s", svc)
		task.Notify = []string{handlerName}
		gen.Handlers = append(gen.Handlers, &ir.Task{
			ID:     newID("h"),
			Name:   handlerName,
			Module: "ansible.builtin.service",
			Args:   map[string]any{"name": svc, "state": "restarted"},
			Become: became(),
		})
	}
	return gen, nil
}

func (renderTemplate) Recognize(t *ir.Task) (map[string]any, bool) {
	// A single notify target maps to "restart this service"; more than one
	// cannot be represented by the curated form, so the task stays
	// Advanced rather than having notifications silently hidden.
	if len(t.Notify) > 1 {
		return nil, false
	}
	args, ok := match(t, []string{"ansible.builtin.template"},
		[]string{"src", "dest", "owner", "group", "mode"}, true)
	if !ok {
		return nil, false
	}
	src, _ := args["src"].(string)
	dest, _ := args["dest"].(string)
	if src == "" || dest == "" {
		return nil, false
	}
	params := map[string]any{"templatePath": src, "dest": dest}
	for _, k := range []string{"owner", "group", "mode"} {
		if v, ok := args[k].(string); ok && v != "" {
			params[k] = v
		}
	}
	if len(t.Notify) == 1 {
		params["restartService"] = stripServiceVerb(t.Notify[0])
	}
	return params, true
}

func (renderTemplate) Describe(params map[string]any) (string, string) {
	dest := str(params, "dest")
	sub := fmt.Sprintf("%s → %s", str(params, "templatePath"), dest)
	if svc := str(params, "restartService"); svc != "" {
		sub += fmt.Sprintf(" · restarts %s when changed", svc)
	}
	return fmt.Sprintf("Deploy %s", path.Base(dest)), sub
}

// stripServiceVerb removes a leading action word from a handler name
// ("Restart nginx" -> "nginx") so the curated service field holds a
// service name. Unknown names are kept as-is.
func stripServiceVerb(name string) string {
	name = strings.TrimSpace(name)
	for _, verb := range []string{"Restart ", "Reload ", "Start ", "Stop ", "Enable "} {
		if strings.HasPrefix(name, verb) {
			return strings.TrimSpace(strings.TrimPrefix(name, verb))
		}
	}
	return name
}

// =====================================================================
// Create a folder
// =====================================================================

type createDirectory struct{}

func (createDirectory) Definition() Definition {
	return Definition{
		ID:          "create-directory",
		Name:        "Create a folder",
		Icon:        "📁",
		Summary:     "Create a directory (and its parents)",
		Explanation: "This creates the folder if it does not exist yet, with the permissions you choose.",
		Fields: []Field{
			{ID: "path", Label: "Folder path", Type: "text", Required: true, Placeholder: "/srv/app/data"},
			{ID: "owner", Label: "Owner", Type: "text", Placeholder: "root"},
			{ID: "group", Label: "Group", Type: "text", Placeholder: "root"},
			{ID: "mode", Label: "Permissions", Type: "text", Default: "0755", Placeholder: "0755"},
		},
	}
}

func (createDirectory) Generate(params map[string]any) (*Generated, error) {
	p := strings.TrimSpace(str(params, "path"))
	if p == "" {
		return nil, fmt.Errorf("Folder path is required.")
	}
	args := map[string]any{"path": p, "state": "directory"}
	if v := strings.TrimSpace(str(params, "owner")); v != "" {
		args["owner"] = v
	}
	if v := strings.TrimSpace(str(params, "group")); v != "" {
		args["group"] = v
	}
	if v := strings.TrimSpace(str(params, "mode")); v != "" {
		args["mode"] = v
	}
	return &Generated{Tasks: []*ir.Task{{
		ID:     newID("t"),
		Name:   fmt.Sprintf("Create folder %s", p),
		Module: "ansible.builtin.file",
		Args:   args,
		Become: became(),
	}}}, nil
}

func (createDirectory) Recognize(t *ir.Task) (map[string]any, bool) {
	args, ok := match(t, []string{"ansible.builtin.file"},
		[]string{"path", "state", "owner", "group", "mode", "recurse", "src"}, false)
	if !ok {
		return nil, false
	}
	if state, _ := args["state"].(string); state != "directory" {
		return nil, false
	}
	p, _ := args["path"].(string)
	if p == "" {
		return nil, false
	}
	params := map[string]any{"path": p}
	for _, k := range []string{"owner", "group", "mode"} {
		if v, ok := args[k].(string); ok && v != "" {
			params[k] = v
		}
	}
	return params, true
}

func (createDirectory) Describe(params map[string]any) (string, string) {
	return fmt.Sprintf("Create folder %s", str(params, "path")), "Directory"
}

// =====================================================================
// Create a link (e.g. enabling an nginx site)
// =====================================================================

type linkFile struct{}

func (linkFile) Definition() Definition {
	return Definition{
		ID:      "link-file",
		Name:    "Link a file",
		Icon:    "🔗",
		Summary: "Enable a file by linking it into place",
		Explanation: "This makes an existing file available at another path — the usual way to " +
			"enable a configuration that ships disabled.",
		Fields: []Field{
			{ID: "dest", Label: "Link path", Type: "text", Required: true,
				Placeholder: "/etc/nginx/sites-enabled/reverse-proxy.conf"},
			{ID: "src", Label: "Points to", Type: "text", Required: true,
				Placeholder: "/etc/nginx/sites-available/reverse-proxy.conf"},
			{ID: "force", Label: "Replace an existing link", Type: "bool", Default: true},
		},
	}
}

func (linkFile) Generate(params map[string]any) (*Generated, error) {
	dest := strings.TrimSpace(str(params, "dest"))
	src := strings.TrimSpace(str(params, "src"))
	if dest == "" || src == "" {
		return nil, fmt.Errorf("A link path and a target are required.")
	}
	args := map[string]any{"state": "link", "dest": dest, "src": src}
	if boolParam(params, "force") {
		args["force"] = true
	}
	return &Generated{Tasks: []*ir.Task{{
		ID:     newID("t"),
		Name:   fmt.Sprintf("Enable %s", path.Base(dest)),
		Module: "ansible.builtin.file",
		Args:   args,
		Become: became(),
	}}}, nil
}

func (linkFile) Recognize(t *ir.Task) (map[string]any, bool) {
	args, ok := match(t, []string{"ansible.builtin.file"},
		[]string{"state", "dest", "path", "src", "force", "owner", "group", "mode"}, false)
	if !ok {
		return nil, false
	}
	if state, _ := args["state"].(string); state != "link" {
		return nil, false
	}
	// `path` is an alias of `dest` in ansible.builtin.file.
	dest, _ := args["dest"].(string)
	if dest == "" {
		dest, _ = args["path"].(string)
	}
	src, _ := args["src"].(string)
	if dest == "" || src == "" {
		return nil, false
	}
	params := map[string]any{"dest": dest, "src": src}
	if force, _ := args["force"].(bool); force {
		params["force"] = true
	}
	return params, true
}

func (linkFile) Describe(params map[string]any) (string, string) {
	return fmt.Sprintf("Enable %s", path.Base(str(params, "dest"))),
		fmt.Sprintf("%s → %s", str(params, "dest"), str(params, "src"))
}

// =====================================================================
// Remove a file or link (e.g. disabling a default configuration)
// =====================================================================

type removePath struct{}

func (removePath) Definition() Definition {
	return Definition{
		ID:      "remove-path",
		Name:    "Remove a file or link",
		Icon:    "🗑",
		Summary: "Delete a file or symbolic link",
		Explanation: "This deletes the file or link if it exists. Removing a configuration file " +
			"is how a default setting is disabled.",
		Fields: []Field{
			{ID: "path", Label: "Path", Type: "text", Required: true,
				Placeholder: "/etc/nginx/sites-enabled/default"},
		},
	}
}

func (removePath) Generate(params map[string]any) (*Generated, error) {
	p := strings.TrimSpace(str(params, "path"))
	if p == "" {
		return nil, fmt.Errorf("A path is required.")
	}
	name := fmt.Sprintf("Remove %s", path.Base(p))
	if strings.Contains(p, "sites-enabled") {
		name = fmt.Sprintf("Disable %s", path.Base(p))
	}
	return &Generated{Tasks: []*ir.Task{{
		ID:     newID("t"),
		Name:   name,
		Module: "ansible.builtin.file",
		Args:   map[string]any{"state": "absent", "path": p},
		Become: became(),
	}}}, nil
}

func (removePath) Recognize(t *ir.Task) (map[string]any, bool) {
	args, ok := match(t, []string{"ansible.builtin.file"},
		[]string{"state", "path"}, false)
	if !ok {
		return nil, false
	}
	if state, _ := args["state"].(string); state != "absent" {
		return nil, false
	}
	p, _ := args["path"].(string)
	if p == "" {
		return nil, false
	}
	return map[string]any{"path": p}, true
}

func (removePath) Describe(params map[string]any) (string, string) {
	p := str(params, "path")
	label := fmt.Sprintf("Remove %s", path.Base(p))
	if strings.Contains(p, "sites-enabled") {
		label = fmt.Sprintf("Disable %s", path.Base(p))
	}
	return label, p
}

// =====================================================================
// Run a command
// =====================================================================

type runCommand struct{}

func (runCommand) Definition() Definition {
	return Definition{
		ID:      "run-command",
		Name:    "Run a command",
		Icon:    "▶",
		Summary: "Run a raw command on the machines",
		Explanation: "A raw command is not aware of the system state, so it runs every time. " +
			"Prefer a purpose-built automation when one exists.",
		Fields: []Field{
			{ID: "command", Label: "Command", Type: "textarea", Required: true,
				Placeholder: "systemctl reload nginx"},
			{ID: "useShell", Label: "Use a shell (allows pipes and redirects)", Type: "bool", Default: false},
			{ID: "sudo", Label: "Run with administrator privileges", Type: "bool", Default: false},
		},
	}
}

func (runCommand) Generate(params map[string]any) (*Generated, error) {
	cmd := strings.TrimSpace(str(params, "command"))
	if cmd == "" {
		return nil, fmt.Errorf("Command is required.")
	}
	module := "ansible.builtin.command"
	if boolParam(params, "useShell") {
		module = "ansible.builtin.shell"
	}
	task := &ir.Task{
		ID:     newID("t"),
		Name:   fmt.Sprintf("Run: %s", truncate(cmd, 60)),
		Module: module,
		Args:   map[string]any{"cmd": cmd},
	}
	if boolParam(params, "sudo") {
		task.Become = became()
	}
	return &Generated{Tasks: []*ir.Task{task}}, nil
}

func (runCommand) Recognize(t *ir.Task) (map[string]any, bool) {
	args, ok := match(t,
		[]string{"ansible.builtin.command", "ansible.builtin.shell"},
		[]string{"cmd", "_raw_params"}, false)
	if !ok {
		return nil, false
	}
	cmd, _ := args["cmd"].(string)
	if cmd == "" {
		cmd, _ = args["_raw_params"].(string)
	}
	if cmd == "" {
		return nil, false
	}
	params := map[string]any{"command": cmd}
	if t.Module == "ansible.builtin.shell" {
		params["useShell"] = true
	}
	if t.Become != nil && *t.Become {
		params["sudo"] = true
	}
	return params, true
}

func (runCommand) Describe(params map[string]any) (string, string) {
	return fmt.Sprintf("Run: %s", truncate(str(params, "command"), 50)), str(params, "command")
}

// =====================================================================
// Docker container (gated on the community.docker collection)
// =====================================================================

type dockerContainer struct{}

func (dockerContainer) Definition() Definition {
	return Definition{
		ID:                 "docker-container",
		Name:               "Docker container",
		Icon:               "🐳",
		Summary:            "Run a container from an image",
		Explanation:        "This makes sure the container exists and is running from the image you choose.",
		RequiresCollection: "community.docker",
		Fields: []Field{
			{ID: "name", Label: "Container name", Type: "text", Required: true, Placeholder: "web"},
			{ID: "image", Label: "Image", Type: "text", Required: true, Placeholder: "nginx:latest"},
			{ID: "ports", Label: "Published ports", Type: "text", Placeholder: "8080:80, 443:443",
				Help: "Comma-separated host:container pairs."},
			{ID: "state", Label: "State", Type: "select", Default: "started", Choices: []Choice{
				{Value: "started", Label: "Running"},
				{Value: "stopped", Label: "Stopped"},
				{Value: "absent", Label: "Removed"},
			}},
			{ID: "restartPolicy", Label: "Restart policy", Type: "select", Default: "unless-stopped", Choices: []Choice{
				{Value: "no", Label: "Never"},
				{Value: "unless-stopped", Label: "Unless manually stopped"},
				{Value: "always", Label: "Always"},
				{Value: "on-failure", Label: "On failure"},
			}},
		},
	}
}

func (dockerContainer) Generate(params map[string]any) (*Generated, error) {
	name := strings.TrimSpace(str(params, "name"))
	image := strings.TrimSpace(str(params, "image"))
	if name == "" || image == "" {
		return nil, fmt.Errorf("Container name and image are required.")
	}
	state := str(params, "state")
	if state == "" {
		state = "started"
	}
	args := map[string]any{"name": name, "image": image, "state": state}
	if ports := strings.TrimSpace(str(params, "ports")); ports != "" {
		var list []string
		for _, p := range strings.Split(ports, ",") {
			if p = strings.TrimSpace(p); p != "" {
				list = append(list, p)
			}
		}
		if len(list) > 0 {
			args["ports"] = list
		}
	}
	if policy := strings.TrimSpace(str(params, "restartPolicy")); policy != "" {
		args["restart_policy"] = policy
	}
	verb := map[string]string{"started": "Run", "stopped": "Stop", "absent": "Remove"}[state]
	if verb == "" {
		verb = "Run"
	}
	return &Generated{Tasks: []*ir.Task{{
		ID:     newID("t"),
		Name:   fmt.Sprintf("%s container %s", verb, name),
		Module: "community.docker.docker_container",
		Args:   args,
		Become: became(),
	}}}, nil
}

func (dockerContainer) Recognize(t *ir.Task) (map[string]any, bool) {
	args, ok := match(t, []string{"community.docker.docker_container"},
		[]string{"name", "image", "ports", "state", "restart_policy"}, false)
	if !ok {
		return nil, false
	}
	name, _ := args["name"].(string)
	image, _ := args["image"].(string)
	if name == "" || image == "" {
		return nil, false
	}
	state, _ := args["state"].(string)
	switch state {
	case "started", "stopped", "absent":
	case "":
		state = "started"
	default:
		return nil, false
	}
	params := map[string]any{"name": name, "image": image, "state": state}
	if ports := argStringList(args, "ports"); len(ports) > 0 {
		params["ports"] = strings.Join(ports, ", ")
	}
	if policy, _ := args["restart_policy"].(string); policy != "" {
		params["restartPolicy"] = policy
	}
	return params, true
}

func (dockerContainer) Describe(params map[string]any) (string, string) {
	name := str(params, "name")
	state := str(params, "state")
	verb := map[string]string{"started": "Run", "stopped": "Stop", "absent": "Remove"}[state]
	if verb == "" {
		verb = "Run"
	}
	return fmt.Sprintf("%s container %s", verb, name), fmt.Sprintf("Image: %s · %s", str(params, "image"), stateLabel("", state))
}
