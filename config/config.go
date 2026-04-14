package config

import (
	"fmt"

	"github.com/spf13/viper"
)

type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Database DatabaseConfig `mapstructure:"database"`
	CORS     CORSConfig     `mapstructure:"cors"`
	Redis    RedisConfig    `mapstructure:"redis"`
}

type ServerConfig struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
}

type DatabaseConfig struct {
	Driver    string `mapstructure:"driver"`
	Host      string `mapstructure:"host"`
	Port      int    `mapstructure:"port"`
	Name      string `mapstructure:"name"`
	User      string `mapstructure:"user"`
	Password  string `mapstructure:"password"`
	Charset   string `mapstructure:"charset"`
	ParseTime bool   `mapstructure:"parse_time"`
	Loc       string `mapstructure:"loc"`
}

type RedisConfig struct {
	Address string `mapstructure:"addr"`
}

type CORSConfig struct {
	AllowOrigins []string `mapstructure:"allow_origins"`
	AllowMethods []string `mapstructure:"allow_methods"`
	AllowHeaders []string `mapstructure:"allow_headers"`
}

// LoadConfig đọc config.yaml
func LoadConfig() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./config")

	if err := viper.ReadInConfig(); err != nil {
		return nil, err
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// DSN build chuỗi kết nối MySQL
func (d DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"%s:%s@tcp(%s:%d)/%s?parseTime=%v&charset=%s&loc=%s&clientFoundRows=true",
		d.User,
		d.Password,
		d.Host,
		d.Port,
		d.Name,
		d.ParseTime,
		d.Charset,
		d.Loc,
	)
}
