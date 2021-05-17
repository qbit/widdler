widdler
=======

widdler is a single binary that serves up
[TiddlyWiki](https://tiddlywiki.com)s.

It can be used to server existing wikis, or to create new ones.

# Installation

```
go get -u suah.dev/widdler
```

# Running

```
mkdir wiki
cd wiki
# OpenBSD:
htpasswd .htpasswd youruser
# or on Linux/macOS
# htpasswd -c -B youruser
widdler 
```

Now open your browser to [http://localhost:8080](http://localhost:8080)

# Creating a new TiddlyWiki

Simply browse to the file name you wish to create. widdler will automatically
create the wiki file based off the current `empty.html` TiddlyWiki version.

# Saving changes

Simply hit the save button!

# TODO
- [ ] Multi-user support
