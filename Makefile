build-i3statusgo:
	go build cmd/i3statusgo.go

clean:
	-rm i3statusgo v1
	
# private target for myself
__local-deploy:
	go install -x -a -v -race cmd/i3statusgo.go

__deploy_test: build-i3statusgo
	./i3statusgo