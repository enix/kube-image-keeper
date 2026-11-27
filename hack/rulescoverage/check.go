package main

import (
	"errors"
	"fmt"
	"regexp"
	"slices"

	"sigs.k8s.io/yaml"
)

// ruleFile is what the check reads of a Prometheus rule file.
type ruleFile struct {
	Groups []struct {
		Rules []struct {
			Alert  string `json:"alert"`
			Record string `json:"record"`
		} `json:"rules"`
	} `json:"groups"`
}

// testFile is what the check reads of a promtool unit test file: what promtool evaluates,
// never a scenario name, a comment or an expected annotation.
type testFile struct {
	Tests []struct {
		AlertRuleTest []struct {
			Alertname string `json:"alertname"`
		} `json:"alert_rule_test"`
		PromqlExprTest []struct {
			Expr string `json:"expr"`
		} `json:"promql_expr_test"`
	} `json:"tests"`
}

// file is one test file, named for the errors.
type file struct {
	name    string
	content []byte
}

// identifier matches the metric names, label names and keywords of a PromQL expression. A
// recorded series is named with colons, which nothing else in an expression holds.
var identifier = regexp.MustCompile(`[a-zA-Z_:][a-zA-Z0-9_:]*`)

// literal matches the string literals of a PromQL expression, double-quoted and single-quoted
// with their escapes, or raw between backticks. A rule named in one, as a label value is, is
// not read: `up{record="kuik:check_lap_ratio"}` reads up.
var literal = regexp.MustCompile("\"(?:[^\"\\\\]|\\\\.)*\"|'(?:[^'\\\\]|\\\\.)*'|`[^`]*`")

// check reports every rule of the rule file that the test files do not evaluate: an alert
// that no alert_rule_test names, a recording rule that no promql_expr_test expression reads.
func check(rules []byte, tests []file) ([]string, error) {
	var parsed ruleFile
	if err := yaml.Unmarshal(rules, &parsed); err != nil {
		return nil, fmt.Errorf("parsing the rule file: %w", err)
	}

	var alerts, records []string
	for _, f := range tests {
		var test testFile
		if err := yaml.Unmarshal(f.content, &test); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", f.name, err)
		}
		for _, t := range test.Tests {
			for _, a := range t.AlertRuleTest {
				alerts = append(alerts, a.Alertname)
			}
			for _, p := range t.PromqlExprTest {
				records = append(records, identifier.FindAllString(literal.ReplaceAllString(p.Expr, `""`), -1)...)
			}
		}
	}

	var problems []string
	found := false
	for _, group := range parsed.Groups {
		for _, rule := range group.Rules {
			found = true
			switch {
			case rule.Alert != "" && !slices.Contains(alerts, rule.Alert):
				problems = append(problems, fmt.Sprintf("alert %s: no alert_rule_test names it", rule.Alert))
			case rule.Record != "" && !slices.Contains(records, rule.Record):
				problems = append(problems, fmt.Sprintf("record %s: no promql_expr_test reads it", rule.Record))
			}
		}
	}
	if !found {
		// An extraction of the rendered rules that went wrong, not a chart without rules.
		return nil, errors.New("the rule file holds no rule")
	}

	return problems, nil
}
