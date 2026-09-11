package artifacts

import (
	"bytes"
	"fmt"
	"golang.org/x/net/html"
	"net/url"
	"os"
	"path"
	"strings"
)

func checkHTML(root *os.Root, p string, b []byte) error {
	doc, err := html.Parse(bytes.NewReader(b))
	if err != nil {
		return err
	}
	var walk func(*html.Node) error
	walk = func(n *html.Node) error {
		if n.Type == html.ElementNode {
			for _, a := range n.Attr {
				var refs []string
				switch {
				case a.Key == "srcset":
					refs = srcsetURLs(a.Val)
				case a.Key == "src" || (n.Data == "link" && a.Key == "href"):
					refs = []string{a.Val}
				}
				for _, ref := range refs {
					if err := checkResource(root, p, ref); err != nil {
						return err
					}
				}

			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if err := walk(c); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(doc)
}

func checkResource(root *os.Root, p, ref string) error {
	if strings.ContainsAny(ref, "<>\"\n\r") || strings.TrimSpace(ref) == "" {
		return fmt.Errorf("malformed resource reference")
	}
	u, err := url.Parse(ref)
	if err != nil {
		return fmt.Errorf("invalid resource URL")
	}
	if u.IsAbs() || u.Host != "" || u.Path == "" {
		return nil
	}
	if strings.HasPrefix(u.Path, "/") {
		return fmt.Errorf("absolute resource reference %q", ref)
	}
	target := path.Join(path.Dir(p), u.Path)
	if !ValidPath(target) {
		return fmt.Errorf("resource escapes workspace")
	}
	st, err := root.Stat(target)
	if err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
		return fmt.Errorf("missing/invalid local resource %q", ref)
	}
	return nil
}

// Candidate URLs are whitespace-delimited; data URLs may contain commas.
// Descriptors are skipped up to the next separator, without fetching anything.
func srcsetURLs(input string) []string {
	var out []string
	for {
		input = strings.TrimLeft(input, " \t\r\n\f,")
		if input == "" {
			break
		}
		i := strings.IndexAny(input, " \t\r\n\f")
		if i < 0 {
			i = len(input)
		}
		token := input[:i]
		input = input[i:]
		out = append(out, strings.TrimRight(token, ","))
		if strings.HasSuffix(token, ",") {
			continue
		}
		if end := strings.IndexByte(input, ','); end >= 0 {
			input = input[end+1:]
		} else {
			break
		}
	}
	return out
}
