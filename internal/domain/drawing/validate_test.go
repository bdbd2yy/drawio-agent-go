package drawing

import (
	"strings"
	"testing"
)

const valid = `<mxfile><diagram><mxGraphModel><root><mxCell id="0"/><mxCell id="1" parent="0"/><mxCell id="a" parent="1" vertex="1"><mxGeometry x="0" y="0" width="100" height="40" as="geometry"/></mxCell><mxCell id="b" parent="1" vertex="1"><mxGeometry width="100" height="40" as="geometry"/></mxCell><mxCell id="e" parent="1" edge="1" source="a" target="b"><mxGeometry relative="1" as="geometry"/></mxCell></root></mxGraphModel></diagram></mxfile>`

func TestValidate(t *testing.T) {
	cases := []struct {
		name, text string
		ok         bool
	}{
		{"valid", valid, true},
		{"duplicate", strings.Replace(valid, `id="b"`, `id="a"`, 1), false},
		{"reference", strings.Replace(valid, `target="b"`, `target="absent"`, 1), false},
		{"parent", strings.Replace(valid, `parent="0"`, `parent="missing"`, 1), false},
		{"cycle", strings.Replace(valid, `id="a" parent="1"`, `id="a" parent="a"`, 1), false},
		{"geometry", strings.Replace(valid, `width="100"`, `width="NaN"`, 1), false},
		{"missing geometry", strings.Replace(valid, `<mxGeometry x="0" y="0" width="100" height="40" as="geometry"/>`, ``, 1), false},
		{"compressed", `<mxfile><diagram>encoded</diagram></mxfile>`, false},
		{"multi page", strings.Replace(valid, `</mxfile>`, `<diagram/></mxfile>`, 1), false},
		{"DTD", `<!DOCTYPE mxfile>` + valid, false},
		{"entity", `<!DOCTYPE mxfile [<!ENTITY x "foo">]>` + valid, false},
		{"base", strings.Replace(valid, `id="0"`, `id="other"`, 1), false},
		{"truncated", valid[:len(valid)-10], false},
		{"multiple roots", valid + valid, false},
		{"missing model", `<mxfile><diagram/></mxfile>`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := Validate(tc.text); (err == nil) != tc.ok {
				t.Fatalf("Validate()=%v, want ok=%v", err, tc.ok)
			}
		})
	}
}
