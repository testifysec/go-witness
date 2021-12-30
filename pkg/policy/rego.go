// Copyright 2021 The Witness Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/open-policy-agent/opa/ast"
	"github.com/open-policy-agent/opa/rego"
	"github.com/testifysec/witness/pkg/attestation"
)

type ErrRegoInvalidData struct {
	Path     string
	Expected string
	Actual   interface{}
}

func (e ErrRegoInvalidData) Error() string {
	return fmt.Sprintf("invalid data from rego at %v, expected %v but got %T", e.Path, e.Expected, e.Actual)
}

type ErrPolicyDenied struct {
	Reasons []string
}

func (e ErrPolicyDenied) Error() string {
	return fmt.Sprintf("policy was denied due to:\n%v", strings.Join(e.Reasons, "\n  -"))
}

func EvaluateRegoPolicy(attestor attestation.Attestor, policies []RegoPolicy) error {
	attestorJson, err := json.Marshal(attestor)
	if err != nil {
		return err
	}

	decoder := json.NewDecoder(bytes.NewReader(attestorJson))
	decoder.UseNumber()
	var input interface{}
	if err := decoder.Decode(&input); err != nil {
		return err
	}

	query := ""
	denyPaths := map[string]struct{}{}
	regoOpts := []func(*rego.Rego){rego.Input(input)}
	for _, policy := range policies {
		policyString := string(policy.Module)
		parsedModule, err := ast.ParseModule(policy.Name, policyString)
		if err != nil {
			return err
		}

		packageDenyPathStr := fmt.Sprintf("%v.deny", parsedModule.Package.Path)
		// if packages share the same name we only want the package to show up once in our query.  rego will merge their deny results
		if _, ok := denyPaths[packageDenyPathStr]; !ok {
			query += fmt.Sprintf("%v.deny\n", parsedModule.Package.Path)
			denyPaths[packageDenyPathStr] = struct{}{}
		}

		regoOpts = append(regoOpts, rego.ParsedModule(parsedModule))
	}

	regoOpts = append(regoOpts, rego.Query(query))
	rego := rego.New(regoOpts...)
	rs, err := rego.Eval(context.Background())
	if err != nil {
		return err
	}

	allDenyReasons := []string{}
	for _, expression := range rs {
		for _, value := range expression.Expressions {
			denyReasons, ok := value.Value.([]interface{})
			if !ok {
				return ErrRegoInvalidData{Path: value.Text, Expected: "[]interface{}", Actual: value.Value}
			}

			for _, reason := range denyReasons {
				reasonStr, ok := reason.(string)
				if !ok {
					return ErrRegoInvalidData{Path: value.Text, Expected: "string", Actual: value.Value}
				}

				allDenyReasons = append(allDenyReasons, reasonStr)
			}
		}
	}

	if len(allDenyReasons) > 0 {
		return ErrPolicyDenied{Reasons: allDenyReasons}
	}

	return nil
}