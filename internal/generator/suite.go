// Copyright (c) 2020-2023 Doc.ai and/or its affiliates.
//
// Copyright (c) 2022-2023 Cisco and/or its affiliates.
//
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at:
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package generator

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"text/template"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

const suiteTemplate = `// Code generated by gotestmd DO NOT EDIT.
package {{ .Name }}

import(
	{{ .Imports }}
)

type Suite struct {
	{{ .Fields }}
}

func (s *Suite) SetupSuite() {
	{{ .Setup }}
	{{ if or .Run .Cleanup }}
	r := s.Runner("{{.Dir}}")
	{{ end }}
	{{ .Cleanup }}
	{{ .Run }}

{{ if .TestIncludedSuites }}
	s.RunIncludedSuites()
}

func (s *Suite) RunIncludedSuites() {
	{{ .TestIncludedSuites }}
{{ end }}
}
`

const includedSuiteTemplate = `
	{{ range .Suites }}
		s.Run("{{ .Title }}", func() {
			suite.Run(s.T(), &s.{{ .Name }}Suite)
		})
	{{ end }}
`

// Body represents a body of the method
type Body []string

// String returns the body as part of the method
func (b Body) String() string {
	var sb strings.Builder

	if len(b) == 0 {
		return ""
	}

	for _, block := range b {
		sb.WriteString("r.Run(")
		var lines = strings.Split(block, "\n")
		for i, line := range lines {
			sb.WriteString("`")
			sb.WriteString(line)
			sb.WriteString("`")
			if i+1 < len(lines) {
				sb.WriteString("+\"\\n\"+")
			}
		}
		sb.WriteString(")\n")
	}

	return sb.String()
}

// BashString returns the body as a bash script for the suite
func (b Body) BashString(withExit, retry bool) string {
	var sb strings.Builder

	if len(b) == 0 {
		return "\t:\n"
	}

	for _, block := range b {
		sb.WriteString("\t")
		if retry {
			sb.WriteString("try_run '")
			sb.WriteString(strings.ReplaceAll(block, "'", "'\\''"))
			sb.WriteString("'")
		} else {
			sb.WriteString(block)
		}
		sb.WriteString("\n")
		if withExit {
			sb.WriteString("\t[ $? = 0 ] || exit 1\n")
		}
	}

	return sb.String()
}

// Suite represents a template for generating a testify suite.Suite
type Suite struct {
	Dir      string
	Location string
	Dependency
	Cleanup     Body
	Run         Body
	Tests       []*Test
	Children    []*Suite
	Parents     []*Suite
	Deps        Dependencies
	DepsToSetup Dependencies
}

func (s *Suite) generateChildrenTesting() string {
	tmpl, err := template.New("test").Parse(includedSuiteTemplate)
	if err != nil {
		panic(err.Error())
	}

	type suiteData struct {
		Title string
		Name  string
	}

	if len(s.Children) == 0 {
		return ""
	}

	var suites []*suiteData
	for _, child := range s.Children {
		_, title := path.Split(child.Dir)
		title = cases.Title(language.Und, cases.NoLower).String(nameRegex.ReplaceAllString(title, "_"))
		suite := &suiteData{
			Title: title,
			Name:  child.Name(),
		}

		suites = append(suites, suite)
	}

	var result = new(strings.Builder)
	err = tmpl.Execute(result, struct {
		Suites []*suiteData
	}{
		Suites: suites,
	})
	if err != nil {
		panic(err.Error())
	}
	return result.String()
}

// String returns a string that contains generated testify.Suite
func (s *Suite) String() string {
	tmpl, err := template.New("test").Parse(
		suiteTemplate,
	)

	if err != nil {
		panic(err.Error())
	}

	cleanup := s.Cleanup.String()
	if len(cleanup) > 0 {
		cleanup = fmt.Sprintf(`	s.T().Cleanup(func() {
		%v
	})`, cleanup)
	}

	var result = new(strings.Builder)

	_ = tmpl.Execute(result, struct {
		Dir                string
		Name               string
		Cleanup            string
		Run                string
		Fields             string
		Imports            string
		Setup              string
		TestIncludedSuites string
	}{
		Dir:                s.Dir,
		Name:               s.Name(),
		Cleanup:            cleanup,
		Run:                s.Run.String(),
		Imports:            s.Deps.String(),
		Fields:             s.Deps.FieldsString(),
		Setup:              s.DepsToSetup.SetupString(),
		TestIncludedSuites: s.generateChildrenTesting(),
	})

	if len(s.Tests) == 0 {
		s.Tests = append(s.Tests, new(Test))
	}

	for _, test := range s.Tests {
		_, _ = result.WriteString(test.String())
	}

	return spaceRegex.ReplaceAllString(strings.TrimSpace(result.String()), "\n")
}

const bashSuiteTemplate = `
#!/usr/bin/env bash
{{ .RetryFunction }}
setup_dependencies() {
{{ .SetupDependencies }}}

setup_main() {
{{ .SetupMain }}}

setup() {
	setup_dependencies && setup_main
}

cleanup_dependencies() {
{{ .CleanupDependencies }}	# cleanup shouldn't report errors
	true
}

cleanup_main() {
{{ .CleanupMain }}	# cleanup shouldn't report errors
	true
}

cleanup() {
	cleanup_main
	cleanup_dependencies
}
`

const retryTemplate = `
function try_run() {
    command="$1"
    attempt=0
    retry_interval=1
    timeout="${RETRY_TIMEOUT_SECONDS:-300}"
    start_time="$(date -u +%s)"
    echo "===== next command ====="
    echo "$command"
    while true; do
        attempt=$((attempt + 1))
        echo "===== attempt $attempt ====="
        echo "current time $(date +"%Y-%m-%dT%H:%M:%S%z")"
        source /dev/stdin <<<"$(echo "${command}")"
        retval=$?
		echo
        echo "retval = $retval"
        current_time="$(date -u +%s)"
        elapsed=$((current_time-start_time))
        echo "elapsed = $elapsed"
        [ $retval = 0 ] && echo "===== command success =====" && return 0
        [ "$elapsed" -gt "$timeout" ] && echo "===== command timed out =====" && return 1
        sleep $retry_interval
    done
}
`

// BashString generates bash script for the suite
func (s *Suite) BashString(retry bool) string {
	var setupDependencies Body
	for _, p := range s.Parents {
		setupDependencies = append(setupDependencies, p.getDependenciesSetup()...)
	}
	var cleanupDependencies Body
	for _, p := range s.Parents {
		cleanupDependencies = append(cleanupDependencies, p.getDependenciesCleanup()...)
	}

	absDir, _ := filepath.Abs(s.Dir)
	s.Run = append([]string{"cd " + absDir}, s.Run...)
	s.Run = append([]string{fmt.Sprintf("echo 'setup suite %s'", filepath.Dir(s.Location))}, s.Run...)
	s.Cleanup = append([]string{"cd " + absDir}, s.Cleanup...)
	s.Cleanup = append([]string{fmt.Sprintf("echo 'cleanup suite %s'", filepath.Dir(s.Location))}, s.Cleanup...)

	tmpl, err := template.New("test").Parse(bashSuiteTemplate)
	if err != nil {
		panic(err.Error())
	}

	var result = new(strings.Builder)

	retryFunction := ""
	if retry {
		retryFunction = retryTemplate
	}
	_ = tmpl.Execute(result, struct {
		Dir                 string
		SetupDependencies   string
		SetupMain           string
		CleanupDependencies string
		CleanupMain         string
		RetryFunction       string
	}{
		Dir:                 absDir,
		SetupDependencies:   setupDependencies.BashString(true, retry),
		SetupMain:           s.Run.BashString(true, retry),
		CleanupDependencies: cleanupDependencies.BashString(false, false),
		CleanupMain:         s.Cleanup.BashString(false, false),
		RetryFunction:       retryFunction,
	})
	for _, test := range s.Tests {
		result.WriteString(test.BashString(retry))
	}
	result.WriteString("\n\n")
	result.WriteString("\"$1\"\n")

	return result.String()
}

func (s *Suite) getDependenciesSetup() []string {
	setup := make([]string, 0)
	for _, p := range s.Parents {
		setup = append(setup, p.getDependenciesSetup()...)
	}

	absDir, _ := filepath.Abs(s.Dir)
	setup = append(setup, fmt.Sprintf("echo 'setup suite %s'", filepath.Dir(s.Location)), "cd "+absDir)
	setup = append(setup, s.Run...)
	return setup
}

func (s *Suite) getDependenciesCleanup() []string {
	absDir, _ := filepath.Abs(s.Dir)
	cleanup := []string{fmt.Sprintf("echo 'cleanup suite %s'", filepath.Dir(s.Location)), "cd " + absDir}
	cleanup = append(cleanup, s.Cleanup...)
	for _, p := range s.Parents {
		cleanup = append(cleanup, p.getDependenciesSetup()...)
	}

	return cleanup
}
