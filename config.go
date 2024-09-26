package main

import (
	"github.com/kkyr/fig"
)

type config struct {
	BaseURL		string	`fig:"base-url" default:"/"`
	SaveLinks	string	`fig:"save-links"`
	Alias		bool	`fig:"alias"`
	CSProtection	bool	`fig:"cs-protection"`
	RateLimit	int64	`fig:"rate-limit" default:"3"`
	AbuseEmail	string  `fig:"abuse-email"`
	Domain		string	`fig:"domain"`
        Network         struct {
		Fcgi	bool	`fig:"fcgi"`
                Type    string  `fig:"type" default:"tcp"`
                Port    int     `fig:"port" default:"9000"`
                Address string  `fig:"address" default:"localhost"`
                Unix    string  `fig:"unix" default:"/run/lang302.sock"`
        }
}
var cfg config

func load() error {
        return fig.Load(&cfg,
                fig.File("simpleshortener.yaml"),
                fig.Dirs(".", "/etc/simpleshortener",
			"/usr/local/etc/simpleshortener"),
        )
}
