package main

/*
  Copyright (c) IBM Corporation 2016, 2018

  Licensed under the Apache License, Version 2.0 (the "License");
  you may not use this file except in compliance with the License.
  You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

  Unless required by applicable law or agreed to in writing, software
  distributed under the License is distributed on an "AS IS" BASIS,
  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
  See the License for the specific

   Contributors:
     Mark Taylor - Initial Contribution
*/

import (
	"net/http"
	"os"

	"github.com/ibm-messaging/mq-golang/ibmmq"
	"github.com/ibm-messaging/mq-golang/mqmetric"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

func initLog() {
	level, err := log.ParseLevel(config.logLevel)
	if err != nil {
		level = log.InfoLevel
	}
	log.SetLevel(level)
}

func main() {
	var err error

	err = initConfig()

	if config.qMgrName == "" {
		log.Errorln("Must provide a queue manager name to connect to.")
		os.Exit(1)
	}

	initLog()
	log.Infoln("Starting IBM MQ metrics exporter for Prometheus monitoring")

	if err == nil {
		// Connect and open standard queues
		err = mqmetric.InitConnection(config.qMgrName, config.replyQ, &config.cc)
		if err == nil {
			log.Infoln("Connected to queue manager ", config.qMgrName)
			defer mqmetric.EndConnection()
		}
	}

	// What metrics can the queue manager provide? Find out, and
	// subscribe.
	if err == nil {
		// Do we need to expand wildcarded queue names
		// or use the wildcard as-is in the subscriptions
		wildcardResource := true
		if config.metaPrefix != "" {
			wildcardResource = false
		}

		mqmetric.SetLocale(config.locale)
		err = mqmetric.DiscoverAndSubscribe(config.monitoredQueues, wildcardResource, config.metaPrefix)
	}

	if err == nil {
		var compCode int32
		compCode, err = mqmetric.VerifyConfig()
		// We could choose to fail after a warning, but instead will continue for now
		if compCode == ibmmq.MQCC_WARNING {
			log.Println(err)
			err = nil
		}
	}

	// Once everything has been discovered, and the subscriptions
	// created, allocate the Prometheus gauges for each resource
	if err == nil {
		log.Debugf("About to allocate gauges")
		allocateGauges()
		log.Debugf("PubSub Gauges allocated")
		allocateChannelStatusGauges()
		log.Debugf("ChannelGauges allocated")
		allocateQStatusGauges()
		log.Debugf("Queue  Gauges allocated")
	}

	// Go into main loop for handling requests from Prometheus
	if err == nil {
		exporter := newExporter()
		prometheus.MustRegister(exporter)

		http.Handle(config.httpMetricPath, prometheus.Handler())
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Write(landingPage())
		})

		log.Infoln("Listening on", config.httpListenPort)
		log.Fatal(http.ListenAndServe(":"+config.httpListenPort, nil))

	}

	if err != nil {
		log.Fatal(err)
	}

	os.Exit(0)
}

/*
landingPage gives a very basic response if someone just connects to our port.
The only link on it jumps to the list of available metrics.
*/
func landingPage() []byte {
	return []byte(
		`<html>
<head><title>IBM MQ metrics exporter for Prometheus</title></head>
<body>
<h1>IBM MQ metrics exporter for Prometheus</h1>
<p><a href='` + config.httpMetricPath + `'>Metrics</a></p>
</body>
</html>
`)
}
