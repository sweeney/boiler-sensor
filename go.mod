module github.com/sweeney/boiler-sensor

go 1.21

require github.com/sweeney/common/iot v0.0.0-00010101000000-000000000000

require (
	github.com/eclipse/paho.mqtt.golang v1.4.3 // indirect
	github.com/gorilla/websocket v1.5.0 // indirect
	github.com/warthog618/go-gpiocdev v0.9.0 // indirect
	golang.org/x/net v0.8.0 // indirect
	golang.org/x/sync v0.1.0 // indirect
	golang.org/x/sys v0.18.0 // indirect
)

replace github.com/sweeney/common/iot => ../common/iot
