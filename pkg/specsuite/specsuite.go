// Package specsuite runs a package's Gherkin features (features/*.feature) as a
// strict godog suite under go test. It is imported only from _test.go files.
//
// Tag selection (docs/DEVELOPMENT-FLOW.md §3):
//
//   - GODOG_TAGS, when set, is the base expression (make step sets
//     "@step-N && ~@draft"); otherwise "~@draft".
//   - @integration scenarios run only when REDIS_ADDR is set and the test is not
//     -short. make step / make verify export REDIS_ADDR and DATABASE_URL.
//   - @e2e scenarios need running services and are excluded unless E2E=1.
//
// Strict mode makes undefined or pending steps fail the run, so a scenario
// without step definitions can never pass by accident.
package specsuite

import (
	"os"
	"testing"

	"github.com/cucumber/godog"
)

// IntegrationEnabled reports whether @integration scenarios should run.
func IntegrationEnabled() bool {
	return !testing.Short() && os.Getenv("REDIS_ADDR") != ""
}

// RedisAddr is the Redis (cluster mode) address for integration scenarios.
func RedisAddr() string { return os.Getenv("REDIS_ADDR") }

// DatabaseURL is the Postgres URL for integration scenarios.
func DatabaseURL() string { return os.Getenv("DATABASE_URL") }

// TagExpression returns the godog tag expression Run uses.
func TagExpression() string {
	expr := os.Getenv("GODOG_TAGS")
	if expr == "" {
		expr = "~@draft"
	}
	if !IntegrationEnabled() {
		expr += " && ~@integration"
	}
	if os.Getenv("E2E") != "1" {
		expr += " && ~@e2e"
	}
	return expr
}

// Run executes features/*.feature in the calling package's directory.
func Run(t *testing.T, initScenario func(*godog.ScenarioContext)) {
	t.Helper()
	expr := TagExpression()
	if !IntegrationEnabled() {
		t.Log("specsuite: @integration scenarios skipped (set REDIS_ADDR and drop -short, or use make verify)")
	}
	suite := godog.TestSuite{
		Name:                t.Name(),
		ScenarioInitializer: initScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{featuresPath()},
			Tags:     expr,
			Strict:   true,
			TestingT: t,
		},
	}
	if code := suite.Run(); code != 0 {
		t.Fatalf("godog suite failed (tags %q, exit %d)", expr, code)
	}
}

// featuresPath is "features" when the suite sits above a features folder, and
// "." when the suite sits in the folder with the .feature files.
func featuresPath() string {
	if fi, err := os.Stat("features"); err == nil && fi.IsDir() {
		return "features"
	}
	return "."
}
