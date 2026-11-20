package kwik

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Unpack decodes a Dean Edwards "eval(function(p,a,c,k,e,d){...}(...))" packed script
// without running any JavaScript. It mirrors the logic of jsbeautifier's unpacker.

var (
	argsRe = regexp.MustCompile(`(?s)\}\s*\(\s*'(.*)',\s*(\d+|\[\]),\s*(\d+),\s*'(.*?)'\.split\('\|'\)`)
	wordRe = regexp.MustCompile(`\b\w+\b`)
)

const (
	alpha62 = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	alpha95 = " !\"#$%&'()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~"
)

func Unpack(script string) (string, error) {
	m := argsRe.FindStringSubmatch(script)
	if m == nil {
		return "", errors.New("not a p,a,c,k,e,d packed script")
	}
	payload, radixStr, symStr := m[1], m[2], m[4]

	radix := 62
	if radixStr != "[]" {
		var err error
		if radix, err = strconv.Atoi(radixStr); err != nil {
			return "", fmt.Errorf("bad radix %q", radixStr)
		}
	}
	if radix < 2 || radix > 95 {
		return "", fmt.Errorf("unsupported radix %d", radix)
	}
	symtab := strings.Split(symStr, "|")

	payload = strings.ReplaceAll(payload, `\\`, `\`)
	payload = strings.ReplaceAll(payload, `\'`, `'`)

	return wordRe.ReplaceAllStringFunc(payload, func(w string) string {
		n, ok := unbase(w, radix)
		if !ok || n < 0 || n >= len(symtab) || symtab[n] == "" {
			return w
		}
		return symtab[n]
	}), nil
}

func unbase(w string, radix int) (int, bool) {
	if radix <= 36 {
		v, err := strconv.ParseInt(w, radix, 32)
		return int(v), err == nil
	}
	alphabet := alpha62
	if radix > 62 {
		alphabet = alpha95
	}
	n := 0
	for _, ch := range w {
		idx := strings.IndexRune(alphabet, ch)
		if idx < 0 || idx >= radix {
			return 0, false
		}
		n = n*radix + idx
		if n > 1<<30 {
			return 0, false
		}
	}
	return n, true
}
