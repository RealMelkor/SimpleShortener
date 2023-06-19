# Simple Shortener

A program to self-host a URL shortener as a web server or as a fcgi program.

[Live-Demo](https://gmi.gd/)

## FastCGI Web servers configuration

To run Simple Shortener as a FastCGI program, the fcgi mode needs to be enabled
first in the simpleshortener.yaml configuration file.

Configuration example for [nginx](https://nginx.org/) :

```nginx
location / {
    fastcgi_pass    127.0.0.1:9000;
    include         fastcgi_params;
}
```

Configuration example for [OpenBSD httpd](https://man.openbsd.org/httpd.8) :

```nginx
location * {
    fastcgi socket tcp localhost 9000
}
```

## Static files

The files in the static folder must be modified before compilation since they
are embedded into the executable.

## Configuration file

The simpleshortener.yaml configuration file needs to be in either the working
directory of the executable, in '/etc/simpleshortener', or in
'/usr/local/etc/simpleshortener'.
