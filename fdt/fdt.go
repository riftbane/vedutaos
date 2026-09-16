// Package fdt reads, edits and writes flattened device trees (.dtb), the description of a
// board the boot loader hands the kernel.
//
// The console edits one board's tree when it builds an image, rather than asking the boot
// loader to apply overlays: what the kernel gets is then a file that can be read back and
// checked. Only what that needs is here: the tree of nodes and properties, labels through
// __symbols__, and phandles.
package fdt

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

const (
	magic = 0xd00dfeed

	tokenBeginNode = 1
	tokenEndNode   = 2
	tokenProp      = 3
	tokenNop       = 4
	tokenEnd       = 9

	headerSize = 40
	version    = 17
	lastComp   = 16
)

// Tree is a whole device tree.
type Tree struct {
	Root     *Node
	Reserved [][2]uint64 // the memory reservation map: address and size
	BootCPU  uint32      // boot_cpuid_phys
}

// Node is a node of the tree. The root's name is empty.
type Node struct {
	Name     string
	Props    []Prop
	Children []*Node
}

// Prop is a property: a name and its raw value.
type Prop struct {
	Name  string
	Value []byte
}

// Parse reads a flattened device tree.
func Parse(b []byte) (*Tree, error) {
	if len(b) < headerSize || binary.BigEndian.Uint32(b) != magic {
		return nil, errors.New("fdt: not a device tree blob")
	}
	h := func(i int) int { return int(binary.BigEndian.Uint32(b[4*i:])) }
	total, offStruct, offStrings, offRsv, ver := h(1), h(2), h(3), h(4), h(5)
	sizeStrings, sizeStruct := h(8), h(9)
	if ver < lastComp || total > len(b) || offStruct+sizeStruct > total || offStrings+sizeStrings > total || offRsv > total {
		return nil, fmt.Errorf("fdt: version %d or sizes out of range", ver)
	}
	t := &Tree{BootCPU: uint32(h(7))}
	for p := offRsv; ; p += 16 {
		if p+16 > total {
			return nil, errors.New("fdt: memory reservation map runs off the end")
		}
		addr, size := binary.BigEndian.Uint64(b[p:]), binary.BigEndian.Uint64(b[p+8:])
		if addr == 0 && size == 0 {
			break
		}
		t.Reserved = append(t.Reserved, [2]uint64{addr, size})
	}
	strs := b[offStrings : offStrings+sizeStrings]
	s := b[offStruct : offStruct+sizeStruct]
	pos := 0
	u32 := func() (uint32, error) {
		if pos+4 > len(s) {
			return 0, errors.New("fdt: structure block runs off the end")
		}
		v := binary.BigEndian.Uint32(s[pos:])
		pos += 4
		return v, nil
	}
	align := func() { pos = (pos + 3) &^ 3 }
	var stack []*Node
	for {
		tok, err := u32()
		if err != nil {
			return nil, err
		}
		switch tok {
		case tokenBeginNode:
			end := bytes.IndexByte(s[pos:], 0)
			if end < 0 {
				return nil, errors.New("fdt: unterminated node name")
			}
			n := &Node{Name: string(s[pos : pos+end])}
			pos += end + 1
			align()
			if len(stack) == 0 {
				if t.Root != nil {
					return nil, errors.New("fdt: more than one root node")
				}
				t.Root = n
			} else {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, n)
			}
			stack = append(stack, n)
		case tokenEndNode:
			if len(stack) == 0 {
				return nil, errors.New("fdt: end of a node that was not begun")
			}
			stack = stack[:len(stack)-1]
		case tokenProp:
			size, err := u32()
			if err != nil {
				return nil, err
			}
			nameOff, err := u32()
			if err != nil {
				return nil, err
			}
			if len(stack) == 0 || pos+int(size) > len(s) || int(nameOff) >= len(strs) {
				return nil, errors.New("fdt: property out of place or out of range")
			}
			end := bytes.IndexByte(strs[nameOff:], 0)
			if end < 0 {
				return nil, errors.New("fdt: unterminated property name")
			}
			n := stack[len(stack)-1]
			n.Props = append(n.Props, Prop{Name: string(strs[nameOff : int(nameOff)+end]), Value: append([]byte(nil), s[pos:pos+int(size)]...)})
			pos += int(size)
			align()
		case tokenNop:
		case tokenEnd:
			if len(stack) != 0 || t.Root == nil {
				return nil, errors.New("fdt: the tree ends inside a node")
			}
			return t, nil
		default:
			return nil, fmt.Errorf("fdt: unknown token %#x", tok)
		}
	}
}

// Bytes writes the tree as a version 17 blob.
func (t *Tree) Bytes() []byte {
	var strs bytes.Buffer
	offsets := map[string]uint32{}
	nameOff := func(name string) uint32 {
		if off, ok := offsets[name]; ok {
			return off
		}
		off := uint32(strs.Len())
		strs.WriteString(name)
		strs.WriteByte(0)
		offsets[name] = off
		return off
	}
	var st bytes.Buffer
	put := func(v uint32) { binary.Write(&st, binary.BigEndian, v) }
	pad := func() {
		for st.Len()%4 != 0 {
			st.WriteByte(0)
		}
	}
	var walk func(n *Node)
	walk = func(n *Node) {
		put(tokenBeginNode)
		st.WriteString(n.Name)
		st.WriteByte(0)
		pad()
		for _, p := range n.Props {
			put(tokenProp)
			put(uint32(len(p.Value)))
			put(nameOff(p.Name))
			st.Write(p.Value)
			pad()
		}
		for _, c := range n.Children {
			walk(c)
		}
		put(tokenEndNode)
	}
	walk(t.Root)
	put(tokenEnd)

	var rsv bytes.Buffer
	for _, r := range append(t.Reserved, [2]uint64{}) {
		binary.Write(&rsv, binary.BigEndian, r[0])
		binary.Write(&rsv, binary.BigEndian, r[1])
	}
	offRsv := uint32(headerSize)
	offStruct := offRsv + uint32(rsv.Len())
	offStrings := offStruct + uint32(st.Len())
	total := offStrings + uint32(strs.Len())
	var out bytes.Buffer
	for _, v := range []uint32{magic, total, offStruct, offStrings, offRsv, version, lastComp, t.BootCPU, uint32(strs.Len()), uint32(st.Len())} {
		binary.Write(&out, binary.BigEndian, v)
	}
	out.Write(rsv.Bytes())
	out.Write(st.Bytes())
	out.Write(strs.Bytes())
	return out.Bytes()
}

// Find returns the node at an absolute path such as /soc/spi@5011000, or nil.
func (t *Tree) Find(path string) *Node {
	if !strings.HasPrefix(path, "/") {
		return nil
	}
	n := t.Root
	for _, part := range strings.Split(strings.Trim(path, "/"), "/") {
		if part == "" {
			continue
		}
		if n = n.Child(part); n == nil {
			return nil
		}
	}
	return n
}

// Label returns the node a label names in /__symbols__, or nil. A tree has symbols only
// when its source was compiled with them (dtc -@).
func (t *Tree) Label(label string) *Node {
	symbols := t.Find("/__symbols__")
	if symbols == nil {
		return nil
	}
	path, ok := symbols.Prop(label)
	if !ok {
		return nil
	}
	return t.Find(string(bytes.TrimRight(path, "\x00")))
}

// Phandle returns the node's phandle, giving it the next free one when it has none.
func (t *Tree) Phandle(n *Node) uint32 {
	if v, ok := n.Prop("phandle"); ok && len(v) == 4 {
		return binary.BigEndian.Uint32(v)
	}
	var highest uint32
	var walk func(*Node)
	walk = func(m *Node) {
		for _, name := range []string{"phandle", "linux,phandle"} {
			if v, ok := m.Prop(name); ok && len(v) == 4 {
				highest = max(highest, binary.BigEndian.Uint32(v))
			}
		}
		for _, c := range m.Children {
			walk(c)
		}
	}
	walk(t.Root)
	n.Set("phandle", Cells(highest+1))
	return highest + 1
}

// Prop returns the value of a property.
func (n *Node) Prop(name string) ([]byte, bool) {
	for _, p := range n.Props {
		if p.Name == name {
			return p.Value, true
		}
	}
	return nil, false
}

// Set gives a property a value, replacing the one it had.
func (n *Node) Set(name string, value []byte) {
	for i, p := range n.Props {
		if p.Name == name {
			n.Props[i].Value = value
			return
		}
	}
	n.Props = append(n.Props, Prop{Name: name, Value: value})
}

// Child returns the child with the name given, or nil.
func (n *Node) Child(name string) *Node {
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// Add returns the child with the name given, adding it when there is none.
func (n *Node) Add(name string) *Node {
	if c := n.Child(name); c != nil {
		return c
	}
	c := &Node{Name: name}
	n.Children = append(n.Children, c)
	return c
}

// Cells encodes 32-bit cells, as <1 2 3> in a source file.
func Cells(v ...uint32) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.BigEndian.PutUint32(b[4*i:], x)
	}
	return b
}

// Strings encodes a string list, as "a", "b" in a source file.
func Strings(s ...string) []byte {
	return []byte(strings.Join(s, "\x00") + "\x00")
}
