package main

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// rules is a rule file with one alert and one recording rule.
const rules = `
groups:
  - name: kuik.pacing
    rules:
      - record: kuik:check_lap_ratio
        expr: vector(1)
      - alert: KuikCheckLapStalled
        expr: vector(1) > 0
`

// covering is a test file that evaluates both rules of rules.
const covering = `
tests:
  - name: both rules
    alert_rule_test:
      - eval_time: 1m
        alertname: KuikCheckLapStalled
    promql_expr_test:
      - expr: max without (name) (kuik:check_lap_ratio) > 0
        eval_time: 1m
`

// one returns content as the only test file, pacing_test.yaml.
func one(content string) []file {
	return []file{{name: "pacing_test.yaml", content: []byte(content)}}
}

// reading returns the only test file, which evaluates the alert of rules and the expression
// expr, written as a block scalar so that its quotes need no escaping.
func reading(expr string) []file {
	return one("tests:\n  - alert_rule_test:\n      - alertname: KuikCheckLapStalled\n" +
		"    promql_expr_test:\n      - expr: |-\n          " + expr + "\n")
}

var _ = Describe("Coverage of the Prometheus rules", func() {
	It("accepts tests that evaluate every alert and every recording rule", func() {
		Expect(check([]byte(rules), one(covering))).To(BeEmpty())
	})

	It("accepts the tests of the chart's own rules", func() {
		chart, err := os.ReadFile("../../helm/kube-image-keeper/files/prometheus-rules.yaml")
		Expect(err).NotTo(HaveOccurred())
		paths, err := filepath.Glob("../../test/rules/*_test.yaml")
		Expect(err).NotTo(HaveOccurred())
		Expect(paths).NotTo(BeEmpty())
		tests := make([]file, 0, len(paths))
		for _, path := range paths {
			content, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())
			tests = append(tests, file{name: path, content: content})
		}
		Expect(check(chart, tests)).To(BeEmpty())
	})

	It("counts the rules evaluated across the test files", func() {
		alerts := "tests:\n  - alert_rule_test:\n      - alertname: KuikCheckLapStalled\n"
		records := "tests:\n  - promql_expr_test:\n      - expr: kuik:check_lap_ratio\n"
		Expect(check([]byte(rules), []file{
			{name: "alerts_test.yaml", content: []byte(alerts)},
			{name: "records_test.yaml", content: []byte(records)},
		})).To(BeEmpty())
	})

	It("reports an alert named only in a comment, a test name and an expected annotation", func() {
		tests := `
# KuikCheckLapStalled is tested below.
tests:
  - name: KuikCheckLapStalled fires
    alert_rule_test:
      - eval_time: 1m
        alertname: KuikCheckLapOverrunning
        exp_alerts:
          - exp_annotations:
              summary: KuikCheckLapStalled
    promql_expr_test:
      - expr: kuik:check_lap_ratio
`
		Expect(check([]byte(rules), one(tests))).To(ConsistOf(
			"alert KuikCheckLapStalled: no alert_rule_test names it"))
	})

	It("reports a recording rule that no expression reads, a longer series name not counting", func() {
		tests := `
# kuik:check_lap_ratio is tested below.
tests:
  - name: kuik:check_lap_ratio
    alert_rule_test:
      - alertname: KuikCheckLapStalled
    promql_expr_test:
      - expr: kuik:check_lap_ratio:max
`
		Expect(check([]byte(rules), one(tests))).To(ConsistOf(
			"record kuik:check_lap_ratio: no promql_expr_test reads it"))
	})

	DescribeTable("reports a recording rule named only in a string literal of an expression",
		func(expr string) {
			Expect(check([]byte(rules), reading(expr))).To(ConsistOf(
				"record kuik:check_lap_ratio: no promql_expr_test reads it"))
		},
		Entry("a double-quoted label value", `up{record="kuik:check_lap_ratio"}`),
		Entry("a single-quoted label value", `up{record='kuik:check_lap_ratio'}`),
		Entry("a label value between backticks", "up{record=`kuik:check_lap_ratio`}"),
		Entry("a label value after one that holds an escaped quote", `up{job="a\"b", record="kuik:check_lap_ratio"}`),
	)

	It("accepts a recording rule read with a label matcher", func() {
		Expect(check([]byte(rules), reading(`kuik:check_lap_ratio{registry="docker.io"} > 1`))).To(BeEmpty())
	})

	It("refuses a rule file that holds no rule", func() {
		_, err := check([]byte("spec:\n  groups: []\n"), one(covering))
		Expect(err).To(MatchError(ContainSubstring("holds no rule")))
	})

	It("refuses a rule file that is not YAML", func() {
		_, err := check([]byte("groups: [unclosed"), nil)
		Expect(err).To(MatchError(ContainSubstring("parsing the rule file")))
	})

	It("refuses a test file that is not YAML, naming it", func() {
		_, err := check([]byte(rules), one("tests: [unclosed"))
		Expect(err).To(MatchError(ContainSubstring("parsing pacing_test.yaml")))
	})
})
