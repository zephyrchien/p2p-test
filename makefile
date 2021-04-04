p2p:
	mkdir -p bin
	cd server && \
	GOOS=linux GOARCH=amd64 go build -o ../bin/server && \
	cd ..

clean:
	rm -rf bin

