all: app-exposer

app-exposer:
	go build -o bin/app-exposer cmd/app-exposer/*.go

clean:
	go clean
	rm bin/app-exposer
