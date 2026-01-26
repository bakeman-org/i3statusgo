build-i3statusgo:
	go build -o i3statusgo cmd/i3statusgo/i3statusgo.go
	
clean:
	rm i3statusgo

# private target for myself
__local-deploy:
	go install cmd/i3statusgo.go
