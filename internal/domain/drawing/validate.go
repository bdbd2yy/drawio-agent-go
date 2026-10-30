// Package drawing validates the supported, uncompressed single-page Draw.io format.
package drawing

import (
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

type cell struct {
	id, parent, source, target string
	vertex, edge               bool
	geometry                   bool
}

func attr(s xml.StartElement, n string) string {
	for _, a := range s.Attr {
		if a.Name.Local == n {
			return a.Value
		}
	}
	return ""
}
func number(s xml.StartElement, n string, positive bool) error {
	v := attr(s, n)
	if v == "" && !positive {
		return nil
	}
	f, e := strconv.ParseFloat(v, 64)
	if e != nil || math.IsNaN(f) || math.IsInf(f, 0) || (positive && f <= 0) {
		return fmt.Errorf("invalid geometry %s=%q", n, v)
	}
	return nil
}

func Validate(text string) error {
	d := xml.NewDecoder(strings.NewReader(text))
	var stack []string
	cells := map[string]*cell{}
	var current *cell
	diagrams, models, roots := 0, 0, 0
	seenRoot := false
	for {
		t, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("invalid XML: %w", err)
		}
		switch t := t.(type) {
		case xml.Directive:
			return fmt.Errorf("DTD and entity declarations are not supported")
		case xml.StartElement:
			if len(stack) >= 128 {
				return fmt.Errorf("XML nesting limit exceeded")
			}
			name := t.Name.Local
			if t.Name.Space != "" {
				return fmt.Errorf("XML namespaces are not supported")
			}
			path := strings.Join(stack, "/")
			if len(stack) == 0 {
				if seenRoot || name != "mxfile" {
					return fmt.Errorf("expected a single mxfile root")
				}
				seenRoot = true
			}
			switch name {
			case "diagram":
				if path != "mxfile" {
					return fmt.Errorf("diagram must be inside mxfile")
				}
				diagrams++
			case "mxGraphModel":
				if path != "mxfile/diagram" {
					return fmt.Errorf("mxGraphModel must be inside diagram")
				}
				models++
			case "root":
				if path != "mxfile/diagram/mxGraphModel" {
					return fmt.Errorf("invalid graph root")
				}
				roots++
			case "mxCell":
				if path != "mxfile/diagram/mxGraphModel/root" {
					return fmt.Errorf("mxCell wrappers are not supported")
				}
				id := attr(t, "id")
				if id == "" || cells[id] != nil {
					return fmt.Errorf("missing or duplicate cell id %q", id)
				}
				current = &cell{id: id, parent: attr(t, "parent"), source: attr(t, "source"), target: attr(t, "target"), vertex: attr(t, "vertex") == "1", edge: attr(t, "edge") == "1"}
				cells[id] = current
			case "mxGeometry":
				if current == nil || len(stack) == 0 || stack[len(stack)-1] != "mxCell" || current.geometry {
					return fmt.Errorf("invalid or duplicate mxGeometry")
				}
				if attr(t, "as") != "geometry" {
					return fmt.Errorf("cell %s geometry requires as=geometry", current.id)
				}
				current.geometry = true
				if current.vertex {
					for _, n := range []string{"x", "y", "width", "height"} {
						if e := number(t, n, n == "width" || n == "height"); e != nil {
							return e
						}
					}
				}
			}
			stack = append(stack, name)
		case xml.EndElement:
			if t.Name.Local == "mxCell" {
				current = nil
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if strings.TrimSpace(string(t)) != "" {
				return fmt.Errorf("text content or compressed diagrams are not supported")
			}
		}
	}
	if !seenRoot || len(stack) != 0 || diagrams != 1 || models != 1 || roots != 1 {
		return fmt.Errorf("expected one uncompressed diagram with mxGraphModel/root")
	}
	if cells["0"] == nil || cells["1"] == nil || cells["0"].parent != "" || cells["1"].parent != "0" {
		return fmt.Errorf("base cells 0 and 1 (parent=0) are required")
	}
	for _, c := range cells {
		if c.vertex && c.edge {
			return fmt.Errorf("cell %s cannot be both vertex and edge", c.id)
		}
		if c.id != "0" && (c.parent == "" || cells[c.parent] == nil) {
			return fmt.Errorf("cell %s has missing parent", c.id)
		}
		if (c.vertex || c.edge) && !c.geometry {
			return fmt.Errorf("cell %s requires mxGeometry", c.id)
		}
		if c.edge {
			for _, id := range []string{c.source, c.target} {
				if cells[id] == nil || !cells[id].vertex {
					return fmt.Errorf("edge %s has invalid vertex reference %q", c.id, id)
				}
			}
		}

	}
	// Linear-time cycle detection across all parent chains.
	colors := map[string]uint8{}
	for id := range cells {
		var path []string
		for p := id; p != ""; p = cells[p].parent {
			if colors[p] == 1 {
				return fmt.Errorf("parent cycle at %s", p)
			}
			if colors[p] == 2 {
				break
			}
			colors[p] = 1
			path = append(path, p)
		}
		for _, p := range path {
			colors[p] = 2
		}
	}
	return nil
}
