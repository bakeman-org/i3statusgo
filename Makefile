build-i3statusgo:
	go build cmd/i3statusgo.go

build-c:
	gcc -O2 -Wall -Wextra -o i3statusgo-c i3statusgo.c

clean-c:
	-rm i3statusgo-c

clean:
	-rm i3statusgo v1
	
# private target for myself
__local-deploy:
	go install -x -a -v -race cmd/i3statusgo.go

__deploy_test: build-i3statusgo
	./i3statusgo