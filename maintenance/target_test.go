package maintenance

import "testing"

func TestValidateTarget(t *testing.T) {
	for _, tc := range []struct {
		name, database, expected string
		redisDB, selected        int
		valid                    bool
	}{
		{"application DB explicitly selected", "convo", "convo", 0, 0, true},
		{"disposable DB explicitly selected", "convo_review_t03", "convo_review_t03", 11, 11, true},
		{"database omitted", "convo", "", 0, 0, false},
		{"redis DB omitted", "convo", "convo", 0, -1, false},
		{"wrong database", "convo", "other", 0, 0, false},
		{"wrong redis DB", "convo", "convo", 0, 11, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateTarget(tc.database, tc.redisDB, tc.expected, tc.selected); (err == nil) != tc.valid {
				t.Fatalf("ValidateTarget error=%v, want valid=%v", err, tc.valid)
			}
		})
	}
}
