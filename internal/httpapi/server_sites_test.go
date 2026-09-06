package httpapi

import "testing"

func TestValidateSiteNameUsesSharedSlugRules(t *testing.T) {
	name, err := validateSiteName("  Marketing-Site ")
	if err != nil || name != "marketing-site" {
		t.Fatalf("validateSiteName() = %q, %v", name, err)
	}
	if _, err := validateSiteName("bad_name"); err == nil {
		t.Fatal("validateSiteName accepted an underscore")
	}
}

func TestParseSiteBuildOptions(t *testing.T) {
	options, err := parseSiteBuildOptions(" PYTHON-3.13 ", "  python -m build  ", "dist/assets")
	if err != nil {
		t.Fatal(err)
	}
	if options.Runtime != "python-3.13" || options.Command != "python -m build" || options.OutputDirectory != "dist/assets" {
		t.Fatalf("unexpected build options: %+v", options)
	}

	defaults, err := parseSiteBuildOptions("", "", "")
	if err != nil || defaults != (siteBuildOptions{}) {
		t.Fatalf("empty options = %+v, %v", defaults, err)
	}

	for _, testCase := range []struct {
		name, runtime, command, output string
	}{
		{name: "missing command", runtime: "node-22", output: "dist"},
		{name: "unknown runtime", runtime: "ruby-3.3", command: "bundle exec rake"},
		{name: "absolute output", command: "npm run build", output: "/dist"},
		{name: "traversal output", command: "npm run build", output: "dist/../public"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := parseSiteBuildOptions(testCase.runtime, testCase.command, testCase.output); err == nil {
				t.Fatal("parseSiteBuildOptions accepted invalid options")
			}
		})
	}
}
