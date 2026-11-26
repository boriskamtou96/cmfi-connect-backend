package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// appLanguages are the languages of the app: a push text must exist in each,
// or the phone silently gets French.
var appLanguages = []string{"ar", "de", "en", "es", "fr", "hi", "zh"}

var pushVerb = regexp.MustCompile(`%[a-z]`)

// pushTextTables finds, in the files of the package, every table of push
// texts: a package-level map[string]string with a "fr" key. New tables are
// found without being listed here.
func pushTextTables(t *testing.T) map[string]map[string]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	tables := map[string]map[string]string{}
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		require.NoError(t, err)
		for _, decl := range parsed.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				value := spec.(*ast.ValueSpec)
				for i, name := range value.Names {
					if i >= len(value.Values) {
						continue
					}
					if texts := stringMap(value.Values[i]); texts != nil && texts["fr"] != "" {
						tables[file+": "+name.Name] = texts
					}
				}
			}
		}
	}
	return tables
}

// stringMap reads a map[string]string literal, nil for anything else.
func stringMap(expr ast.Expr) map[string]string {
	literal, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil
	}
	kind, ok := literal.Type.(*ast.MapType)
	if !ok || !isString(kind.Key) || !isString(kind.Value) {
		return nil
	}
	texts := map[string]string{}
	for _, element := range literal.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			return nil
		}
		key, okKey := pair.Key.(*ast.BasicLit)
		text, okText := pair.Value.(*ast.BasicLit)
		if !okKey || !okText {
			return nil
		}
		k, err1 := strconv.Unquote(key.Value)
		v, err2 := strconv.Unquote(text.Value)
		if err1 != nil || err2 != nil {
			return nil
		}
		texts[k] = v
	}
	return texts
}

func isString(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "string"
}

func verbsOf(text string) []string {
	verbs := pushVerb.FindAllString(text, -1)
	slices.Sort(verbs)
	return verbs
}

func TestPushTextsExistInEveryLanguageOfTheApp(t *testing.T) {
	tables := pushTextTables(t)
	require.GreaterOrEqual(t, len(tables), 27, "the tables of push texts were not found")

	for name, texts := range tables {
		languages := make([]string, 0, len(texts))
		for language := range texts {
			languages = append(languages, language)
		}
		slices.Sort(languages)
		require.Equal(t, appLanguages, languages, "%s: the languages of the app", name)

		for language, text := range texts {
			require.NotEmpty(t, strings.TrimSpace(text), "%s: empty in %s", name, language)
			// the name or the number put in the text must have its place in
			// each language
			require.Equal(t, verbsOf(texts["fr"]), verbsOf(text), "%s: the %%s and %%d in %s", name, language)
		}
	}
}
