.PHONY: dev dist styles

dev:
	xcaddy build --with github.com/Rex--/caddy-git-server=.

dist:
	xcaddy build --with github.com/Rex--/caddy-git-server=. --with github.com/caddy-dns/cloudflare

styles:
	tailwindcss -i tailwind.css -o assets/style.css --minify
