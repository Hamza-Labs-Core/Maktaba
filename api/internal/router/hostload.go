package router

import "github.com/Hamza-Labs-Core/Maktaba/api/internal/hostload"

// HostLoad is the load of the machine this API runs on, read once for the Queues page's
// pulse and the System health page alike.
var HostLoad = hostload.New()
