p2p:
	mkdir -p bin
	cd server && \
	GOOS=linux GOARCH=amd64 go build -o ../bin/server && \
	cd ..
	cd client && \
	GOOS=linux GOARCH=amd64 go build -o ../bin/client -tags=unix && \
	GOOS=windows GOARCH=amd64 go build -o ../bin/client.exe -tags=win && \
	cd ..
	cd echo && \
	GOOS=linux GOARCH=amd64 go build -o ../bin/echo && \
	cd ..
clean:
	rm -rf bin

