build-i3statusgo:
	go build cmd/i3statusgo.go

clean:
	-rm i3statusgo v1
	

# private target for myself
__local-deploy:
	go install cmd/i3statusgo.go
