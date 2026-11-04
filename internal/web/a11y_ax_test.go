//go:build dev

package web

import (
	"encoding/json"
	"fmt"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// a11yNamedRoles are the roles that must have an accessible name.
var a11yNamedRoles = map[string]bool{
	"button": true, "link": true, "textbox": true, "searchbox": true, "checkbox": true, "radio": true,
	"switch": true, "combobox": true, "listbox": true, "slider": true, "spinbutton": true, "tab": true,
	"menuitem": true, "menuitemcheckbox": true, "menuitemradio": true, "option": true, "image": true, "img": true,
}

type a11yAXNode struct {
	Ignored bool `json:"ignored"`
	Role    struct {
		Value string `json:"value"`
	} `json:"role"`
	Name struct {
		Value string `json:"value"`
	} `json:"name"`
	BackendDOMNodeID int `json:"backendDOMNodeId"`
	Properties       []struct {
		Name  string `json:"name"`
		Value struct {
			Value json.RawMessage `json:"value"`
		} `json:"value"`
	} `json:"properties"`
}

// a11yAX reads Chromium's own accessibility tree (the names and roles a screen reader gets) and
// returns the findings h1-count, main-count and name-missing. Page, Role and Width are filled by the caller.
func a11yAX(tab *cdp.Tab) ([]a11yFinding, error) {
	if err := tab.Call("Accessibility.enable", nil, nil); err != nil {
		return nil, err
	}
	if err := tab.Call("DOM.enable", nil, nil); err != nil {
		return nil, err
	}
	if err := tab.Call("DOM.getDocument", map[string]any{"depth": 0}, nil); err != nil {
		return nil, err
	}
	var tree struct {
		Nodes []a11yAXNode `json:"nodes"`
	}
	if err := tab.Call("Accessibility.getFullAXTree", nil, &tree); err != nil {
		return nil, err
	}
	defer tab.Call("Runtime.releaseObjectGroup", map[string]any{"objectGroup": "a11y"}, nil)

	var out []a11yFinding
	h1, mains := 0, 0
	for _, n := range tree.Nodes {
		if n.Ignored {
			continue
		}
		switch n.Role.Value {
		case "main":
			mains++
		case "heading":
			for _, p := range n.Properties {
				if p.Name == "level" && string(p.Value.Value) == "1" {
					h1++
				}
			}
		}
		if a11yNamedRoles[n.Role.Value] && n.Name.Value == "" {
			f := a11yFinding{Check: "name-missing", Detail: fmt.Sprintf("role %s has no accessible name", n.Role.Value)}
			f.Selector, f.Text, f.Mark = a11yAXSelector(tab, n)
			out = append(out, f)
		}
	}
	if h1 != 1 {
		out = append(out, a11yFinding{Check: "h1-count", Selector: "document", Detail: fmt.Sprintf("%d level-1 headings, want 1", h1)})
	}
	if mains != 1 {
		out = append(out, a11yFinding{Check: "main-count", Selector: "document", Detail: fmt.Sprintf("%d main landmarks, want 1", mains)})
	}
	return out, nil
}

// a11yAXSelector names the element behind an accessibility node with the same selector function the
// DOM checks use. When the node has no element (generated content), it falls back to `role "name"`.
func a11yAXSelector(tab *cdp.Tab, n a11yAXNode) (selector, text, mark string) {
	fallback := fmt.Sprintf("%s %q", n.Role.Value, n.Name.Value)
	if n.BackendDOMNodeID == 0 {
		return fallback, "", ""
	}
	var resolved struct {
		Object struct {
			ObjectID string `json:"objectId"`
		} `json:"object"`
	}
	if err := tab.Call("DOM.resolveNode", map[string]any{"backendNodeId": n.BackendDOMNodeID, "objectGroup": "a11y"}, &resolved); err != nil || resolved.Object.ObjectID == "" {
		return fallback, "", ""
	}
	var called struct {
		Result struct {
			Value struct {
				Selector, Text, Mark string
			} `json:"value"`
		} `json:"result"`
	}
	fn := "function() {\n" + a11yRead("common.js") + "\nconst el = this.nodeType === 1 ? this : this.parentElement;\n" +
		"return {Selector: a11ySelector(el), Text: a11yText(el), Mark: a11yCase(el)};\n}"
	err := tab.Call("Runtime.callFunctionOn", map[string]any{"objectId": resolved.Object.ObjectID, "functionDeclaration": fn, "returnByValue": true}, &called)
	if err != nil || called.Result.Value.Selector == "" {
		return fallback, "", ""
	}
	v := called.Result.Value
	return v.Selector, v.Text, v.Mark
}
