package maintenance

import "fmt"

// ValidateTarget requires the operator to name the configured destination.
// Test harnesses keep their own disposable-resource restrictions; an actual
// maintenance command must also support an explicitly selected application DB.
func ValidateTarget(database string, redisDB int, expectedDatabase string, expectedRedisDB int) error {
	if expectedDatabase == "" || expectedRedisDB < 0 {
		return fmt.Errorf("--database and --redis-db must explicitly name the maintenance target")
	}
	if database != expectedDatabase || redisDB != expectedRedisDB {
		return fmt.Errorf("maintenance target does not match configured MySQL database/Redis DB")
	}
	return nil
}
