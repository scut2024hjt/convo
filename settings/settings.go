package settings

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Conf 全局变量，用来保存程序的所有配置信息
var Conf = new(Config)

type Config struct {
	*AppConfig             `mapstructure:"app"`
	*LogConfig             `mapstructure:"log"`
	*MySQLConfig           `mapstructure:"mysql"`
	*RedisConfig           `mapstructure:"redis"`
	*SnowFlakeConfig       `mapstructure:"snowflake"`
	*GinConfig             `mapstructure:"gin"`
	*AuthConfig            `mapstructure:"auth"`
	*RabbitMQConfig        `mapstructure:"rabbitmq"`
	*CacheConfig           `mapstructure:"cache"`
	*RateLimitConfig       `mapstructure:"ratelimit"`
	*ContentDeliveryConfig `mapstructure:"content_delivery"`
}

type AppConfig struct {
	Name    string `mapstructure:"name"`
	Mode    string `mapstructure:"mode"`
	Version string `mapstructure:"version"`
	Port    int    `mapstructure:"port"`
}

type LogConfig struct {
	Level      string `mapstructure:"level"`
	Filename   string `mapstructure:"filename"`
	MaxSize    int    `mapstructure:"max_size"`
	MaxAge     int    `mapstructure:"max_age"`
	MaxBackups int    `mapstructure:"max_backups"`
}

type MySQLConfig struct {
	Host              string `mapstructure:"host"`
	User              string `mapstructure:"user"`
	Password          string `mapstructure:"password"`
	DB                string `mapstructure:"db"`
	Port              int    `mapstructure:"port"`
	MaxOpenConnection int    `mapstructure:"max_open_connection"`
	MaxIdleConnection int    `mapstructure:"max_idle_connection"`
}

type RedisConfig struct {
	Host     string `mapstructure:"host"`
	Password string `mapstructure:"password"`
	Port     int    `mapstructure:"port"`
	DB       int    `mapstructure:"db"`
	PoolSize int    `mapstructure:"pool_size"`
}

type SnowFlakeConfig struct {
	StartTime string `mapstructure:"start_time"`
	MachineId int64  `mapstructure:"machine_id"`
}

type GinConfig struct {
	Mode string `mapstructure:"mode"`
}

type AuthConfig struct {
	JwtExpire int    `mapstructure:"jwt_expire"`
	JwtSecret string `mapstructure:"jwt_secret"`
}

type RabbitMQConfig struct {
	URL                          string `mapstructure:"url"`
	Exchange                     string `mapstructure:"exchange"`
	VoteQueue                    string `mapstructure:"vote_queue"`
	PublishConfirmTimeoutSeconds int    `mapstructure:"publish_confirm_timeout_seconds"`
	ConsumerPrefetch             int    `mapstructure:"consumer_prefetch"`
	MaxRetries                   int    `mapstructure:"max_retries"`
	RetryDelayMilliseconds       int    `mapstructure:"retry_delay_milliseconds"`
}

type CacheConfig struct {
	PostDetailTTLSeconds      int `mapstructure:"post_detail_ttl_seconds"`
	DelayedDeleteMilliseconds int `mapstructure:"delayed_delete_milliseconds"`
}

// RateLimitConfig 投票接口的用户级滑动窗口限流参数。
type RateLimitConfig struct {
	VoteRateLimitWindowMilliseconds int   `mapstructure:"vote_window_milliseconds"`
	VoteRateLimitMaxRequests        int64 `mapstructure:"vote_max_requests"`
}

// ContentDeliveryConfig are conservative development defaults, not production
// throughput guarantees. Keep a finite processing budget below lease expiry.
type ContentDeliveryConfig struct {
	PollMilliseconds  int `mapstructure:"poll_milliseconds"`
	BatchSize         int `mapstructure:"batch_size"`
	LeaseSeconds      int `mapstructure:"lease_seconds"`
	IOTimeoutSeconds  int `mapstructure:"io_timeout_seconds"`
	TaskBudgetSeconds int `mapstructure:"task_budget_seconds"`
	MaxAttempts       int `mapstructure:"max_attempts"`
	MaxBackoffSeconds int `mapstructure:"max_backoff_seconds"`
	IndexWorkers      int `mapstructure:"index_workers"`
	FanoutWorkers     int `mapstructure:"fanout_workers"`
}

func Init() (err error) {
	for key, value := range map[string]interface{}{
		"content_delivery.poll_milliseconds":   500,
		"content_delivery.batch_size":          100,
		"content_delivery.lease_seconds":       15,
		"content_delivery.io_timeout_seconds":  5,
		"content_delivery.task_budget_seconds": 10,
		"content_delivery.max_attempts":        12,
		"content_delivery.max_backoff_seconds": 60,
		"content_delivery.index_workers":       1,
		"content_delivery.fanout_workers":      1,
	} {
		viper.SetDefault(key, value)
	}
	viper.SetConfigName("config") // 指定配置文件名称（不需要带后缀）
	viper.SetConfigType("yaml")   // 指定配置文件类型
	viper.SetEnvPrefix("CONVO")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
	for _, key := range []string{
		"app.mode", "app.port",
		"mysql.host", "mysql.port", "mysql.user", "mysql.password", "mysql.db",
		"redis.host", "redis.port", "redis.password", "redis.db",
		"rabbitmq.url", "rabbitmq.exchange", "rabbitmq.vote_queue",
		"rabbitmq.publish_confirm_timeout_seconds", "rabbitmq.consumer_prefetch",
		"rabbitmq.max_retries", "rabbitmq.retry_delay_milliseconds",
		"auth.jwt_secret", "auth.jwt_expire", "snowflake.machine_id",
		"cache.post_detail_ttl_seconds", "cache.delayed_delete_milliseconds",
		"ratelimit.vote_window_milliseconds", "ratelimit.vote_max_requests",
		"content_delivery.poll_milliseconds", "content_delivery.batch_size",
		"content_delivery.lease_seconds", "content_delivery.io_timeout_seconds",
		"content_delivery.task_budget_seconds", "content_delivery.max_attempts",
		"content_delivery.max_backoff_seconds", "content_delivery.index_workers",
		"content_delivery.fanout_workers",
	} {
		if bindErr := viper.BindEnv(key); bindErr != nil {
			return bindErr
		}
	}
	//viper.AddConfigPath(".")      // 指定查找配置文件的路径（这里使用相对路径）
	viper.AddConfigPath("./conf/")
	err = viper.ReadInConfig() // 读取配置信息
	if err != nil {
		// 读取配置信息失败
		fmt.Printf("viper.AddConfigPath() failed, err: %v\n", err)
		return
	}
	// 把读取到的信息反序列化到 Conf 变量中
	if err := viper.Unmarshal(Conf); err != nil {
		return fmt.Errorf("unmarshal config: %w", err)
	}
	return Conf.Validate()
}

func (config *Config) Validate() error {
	if config.AppConfig == nil || config.MySQLConfig == nil || config.RedisConfig == nil ||
		config.SnowFlakeConfig == nil || config.GinConfig == nil || config.LogConfig == nil ||
		config.AuthConfig == nil || config.RabbitMQConfig == nil || config.CacheConfig == nil ||
		config.RateLimitConfig == nil || config.ContentDeliveryConfig == nil {
		return fmt.Errorf("required config section is missing")
	}
	if config.JwtExpire <= 0 || strings.TrimSpace(config.JwtSecret) == "" {
		return fmt.Errorf("auth.jwt_expire and auth.jwt_secret must be configured")
	}
	if strings.EqualFold(config.AppConfig.Mode, "prod") && config.JwtSecret == "change-me-in-production" {
		return fmt.Errorf("auth.jwt_secret must be supplied for production")
	}
	if config.MachineId < 0 || config.MachineId > 1023 {
		return fmt.Errorf("snowflake.machine_id must be between 0 and 1023")
	}
	if config.RabbitMQConfig.URL == "" || config.RabbitMQConfig.Exchange == "" || config.RabbitMQConfig.VoteQueue == "" {
		return fmt.Errorf("rabbitmq url, exchange and vote_queue must be configured")
	}
	if config.PublishConfirmTimeoutSeconds <= 0 || config.ConsumerPrefetch <= 0 ||
		config.MaxRetries < 0 || config.RetryDelayMilliseconds <= 0 {
		return fmt.Errorf("rabbitmq retry, prefetch and confirm settings are invalid")
	}
	if config.PostDetailTTLSeconds <= 0 || config.DelayedDeleteMilliseconds < 0 {
		return fmt.Errorf("cache ttl and delayed delete settings are invalid")
	}
	if config.VoteRateLimitWindowMilliseconds <= 0 || config.VoteRateLimitMaxRequests <= 0 {
		return fmt.Errorf("ratelimit window and max requests must be positive")
	}
	c := config.ContentDeliveryConfig
	if c.PollMilliseconds < 50 || c.BatchSize < 1 || c.BatchSize > 1000 ||
		c.LeaseSeconds < 3 || c.IOTimeoutSeconds < 1 || c.TaskBudgetSeconds < 1 ||
		c.IOTimeoutSeconds >= c.LeaseSeconds || c.TaskBudgetSeconds >= c.LeaseSeconds ||
		c.MaxAttempts < 1 || c.MaxBackoffSeconds < 1 ||
		c.IndexWorkers < 1 || c.IndexWorkers > 16 || c.FanoutWorkers < 1 || c.FanoutWorkers > 16 {
		return fmt.Errorf("content_delivery: invalid worker, lease or batch configuration")
	}
	return nil
}
