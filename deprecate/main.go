// Copyright (C) 2024 Storj Labs, Inc.
// See LICENSE for copying information.

package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if d.IsDir() {
			processDir(path)
		}
		return nil
	})
	if err != nil {
		panic(err)
	}

}

func processDir(path string) {
	goFile := pickAGoFile(path)
	if goFile == "" {
		return
	}
	pkgName := findPackage(goFile)
	deprecation := `// Copyright (C) 2024 Storj Labs, Inc.
// See LICENSE for copying information.

// Deprecated: Use storj.io/common/PACKAGE instead.
package PACKAGE
`
	deprecation = strings.ReplaceAll(deprecation, "PACKAGE", pkgName)
	fmt.Println(path, pkgName)
	err := os.WriteFile(filepath.Join(path, "deprecation.go"), []byte(deprecation), 0644)
	if err != nil {
		panic(err)
	}
}

func findPackage(file string) string {
	raw := Must(os.ReadFile(file))
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "package") {
			pkgName := strings.TrimSpace(strings.TrimPrefix(line, "package"))
			if !strings.Contains(pkgName, "_test") {
				return pkgName
			}
		}
	}
	panic("Didn't find the package name " + file)
}

func pickAGoFile(path string) string {
	for _, entry := range Must(os.ReadDir(path)) {
		if strings.HasSuffix(entry.Name(), ".go") && !strings.Contains(entry.Name(), "_test") {
			return filepath.Join(path, entry.Name())
		}
	}
	return ""
}

func Must[T any](t T, err error) T {
	if err != nil {
		panic(err)
	}
	return t
}
