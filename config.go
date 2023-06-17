package main

import (
	"github.com/kkyr/fig"
)

type config struct {
	BaseURL		string	`fig:"base-url" default:"/"`
	Captcha		bool	`fig:"captcha"`
	PoWChallenge	bool	`fig:"pow-challenge"`
	SaveLinks	string	`fig:"save-links"`
        Network         struct {
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
