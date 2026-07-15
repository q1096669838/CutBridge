package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strings"
)

type Node struct {
	Name     string
	Attr     map[string]string
	Text     strings.Builder
	Children []*Node
	Parent   *Node
}

func parseDOM(path string) (*Node, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := xml.NewDecoder(f)
	var root *Node
	var stack []*Node
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("XML 解析失败：%w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &Node{Name: t.Name.Local, Attr: map[string]string{}}
			for _, a := range t.Attr {
				n.Attr[a.Name.Local] = a.Value
			}
			if len(stack) > 0 {
				n.Parent = stack[len(stack)-1]
				n.Parent.Children = append(n.Parent.Children, n)
			} else {
				root = n
			}
			stack = append(stack, n)
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].Text.Write([]byte(t))
			}
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if root == nil {
		return nil, fmt.Errorf("XML 为空")
	}
	return root, nil
}

func (n *Node) Direct(name string) *Node {
	if n == nil {
		return nil
	}
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func (n *Node) Find(path string) *Node {
	cur := n
	for _, part := range strings.Split(path, "/") {
		if cur == nil {
			return nil
		}
		cur = cur.Direct(part)
	}
	return cur
}

func (n *Node) TextAt(path, def string) string {
	child := n.Find(path)
	if child == nil {
		return def
	}
	s := strings.TrimSpace(child.Text.String())
	if s == "" {
		return def
	}
	return s
}

func (n *Node) DirectAll(name string) []*Node {
	if n == nil {
		return nil
	}
	out := []*Node{}
	for _, c := range n.Children {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

func (n *Node) FindAll(path string) []*Node {
	parts := strings.Split(path, "/")
	current := []*Node{n}
	for _, part := range parts {
		next := []*Node{}
		for _, cur := range current {
			next = append(next, cur.DirectAll(part)...)
		}
		current = next
	}
	return current
}

func (n *Node) Descendants(name string) []*Node {
	out := []*Node{}
	var walk func(*Node)
	walk = func(cur *Node) {
		for _, c := range cur.Children {
			if c.Name == name {
				out = append(out, c)
			}
			walk(c)
		}
	}
	walk(n)
	return out
}

func (n *Node) Count() int {
	if n == nil {
		return 0
	}
	total := 1
	for _, c := range n.Children {
		total += c.Count()
	}
	return total
}
