package cli

import (
	"strings"
	"testing"

	"github.com/JtheGunner/ticktack/internal/resolver"
)

func TestExplainRuleTextNamesTheStandardTaskReason(t *testing.T) {
	for _, rule := range []resolver.RuleKind{resolver.RuleProjectDefault, resolver.RuleGlobalDefault} {
		d := resolver.Decision{Rule: rule, Reason: "no project named widgets"}
		got := msgExplainRuleText(d, "GEN:default")
		if strings.Contains(got, "no rule matched") || !strings.Contains(got, "no project named widgets") {
			t.Errorf("rule %v: text = %q, want the decision reason", rule, got)
		}
	}
}
