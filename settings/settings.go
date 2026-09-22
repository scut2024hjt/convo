package settings

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Conf 全局变量，用来保存程序的所有配置信息
var Conf = new(Config)

type Config struct {
	*AppConfig       `mapstructure:"app"`
	*LogConfig       `mapstructure:"log"`
	*MySQLConfig     `mapstructure:"mysql"`
	*RedisConfig     `mapstructure:"redis"`
	*SnowFlakeConfig `mapstructure:"snowflake"`
	*GinConfig       `mapstructure:"gin"`
	*AuthConfig      `mapstructure:"auth"`
	*RabbitMQConfig  `mapstructure:"rabbitmq"`
	*CacheConfig     `mapstructure:"cache"`
	*RateLimitConfig `mapstructure:"ratelimit"`
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

func Init() (err error) {
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
		config.RateLimitConfig == nil {
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
	return nil
}
