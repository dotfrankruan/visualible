package render

import (
	"fmt"

	"github.com/dotfrankruan/visualible/internal/ir"
	"gopkg.in/yaml.v3"
)

// Inventory renders an IR inventory into Ansible YAML inventory format:
//
//	all:
//	  hosts:            # ungrouped hosts
//	    stray: {}
//	  children:
//	    web:
//	      hosts:
//	        node01:
//	          ansible_host: 192.168.1.10
//	          ansible_user: debian
//	          ansible_port: 22
//	      vars: {...}
//	    parent:
//	      children:
//	        web: {}
//
// Credentials are never inlined: they live in the credential store and are
// materialized by the deployment backend only when explicitly needed.
func Inventory(inv *ir.Inventory) ([]byte, error) {
	if err := inv.Validate(); err != nil {
		return nil, fmt.Errorf("cannot render invalid inventory: %w", err)
	}
	all := map[string]any{}
	if hosts := renderHosts(inv.Hosts); len(hosts) > 0 {
		all["hosts"] = hosts
	}
	children := map[string]any{}
	for _, g := range inv.Groups {
		children[g.Name] = renderGroup(g)
	}
	if len(children) > 0 {
		all["children"] = children
	}
	doc := map[string]any{"all": all}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal inventory: %w", err)
	}
	return out, nil
}

func renderGroup(g *ir.InventoryGroup) map[string]any {
	m := map[string]any{}
	if hosts := renderHosts(g.Hosts); len(hosts) > 0 {
		m["hosts"] = hosts
	}
	if len(g.Vars) > 0 {
		m["vars"] = g.Vars
	}
	if len(g.Children) > 0 {
		children := map[string]any{}
		for _, c := range g.Children {
			children[c.Name] = renderGroup(c)
		}
		m["children"] = children
	}
	return m
}

func renderHosts(hosts []*ir.Host) map[string]any {
	if len(hosts) == 0 {
		return nil
	}
	m := map[string]any{}
	for _, h := range hosts {
		entry := map[string]any{}
		if h.Address != "" {
			entry["ansible_host"] = h.Address
		}
		if h.SSHUser != "" {
			entry["ansible_user"] = h.SSHUser
		}
		if h.SSHPort > 0 {
			entry["ansible_port"] = h.SSHPort
		}
		for k, v := range h.Vars {
			entry[k] = v
		}
		m[h.Name] = entry
	}
	return m
}
