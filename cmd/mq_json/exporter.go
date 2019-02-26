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

/*
The Collect() function is the key operation
invoked at the configured intervals, causing us to read available publications
and update the various data points.
*/

import (
	"encoding/json"
	"fmt"
	"github.com/ibm-messaging/mq-golang/ibmmq"
	"github.com/ibm-messaging/mq-golang/mqmetric"
	log "github.com/sirupsen/logrus"
	"strings"
	"time"
)

var (
	first          = true
	errorCount     = 0
	lastPoll       = time.Now()
	platformString = ""
)

const (
	blankString = "                                "
)

type collectionTimeStruct struct {
	TimeStamp string `json:"timeStamp"`
	Epoch     int64  `json:"epoch"`
}

type pointsStruct struct {
	ObjectType string             `json:"objectType"`
	Tags       map[string]string  `json:"tags"`
	Metric     map[string]float64 `json:"metrics"`
}

type jsonReportStruct struct {
	CollectionTime collectionTimeStruct `json:"collectionTime"`
	Points         []pointsStruct       `json:"points"`
}

/*
Collect is called by the main routine at regular intervals to provide current
data
*/
func Collect() error {
	var err error
	var j jsonReportStruct

	log.Infof("IBM MQ JSON collector started")

	// Do we need to poll for object status on this iteration
	pollStatus := false
	thisPoll := time.Now()
	elapsed := thisPoll.Sub(lastPoll)
	if elapsed >= config.pollIntervalDuration {
		log.Debugf("Polling for object status")
		lastPoll = thisPoll
		pollStatus = true
	}

	if platformString == "" {
		platformString = strings.Replace(ibmmq.MQItoString("PL", int(mqmetric.GetPlatform())), "MQPL_", "", -1)
	}
	// Clear out everything we know so far. In particular, replace
	// the map of values for each object so the collection starts
	// clean.
	for _, cl := range mqmetric.Metrics.Classes {
		for _, ty := range cl.Types {
			for _, elem := range ty.Elements {
				elem.Values = make(map[string]int64)
			}
		}
	}

	// Process all the publications that have arrived
	mqmetric.ProcessPublications()

	// If there has been sufficient interval since the last explicit poll for
	// status, then do that collection too
	if pollStatus {
		err := mqmetric.CollectChannelStatus(config.monitoredChannels)
		if err != nil {
			log.Errorf("Error collecting channel status: %v", err)
		} else {
			log.Debugf("Collected all channel status")
		}
		if config.qStatus {
			err = mqmetric.CollectQueueStatus(config.monitoredQueues)
			if err != nil {
				log.Errorf("Error collecting queue status: %v", err)
			} else {
				log.Debugf("Collected all queue status")
			}
		}
	}

	// Have now processed all of the publications, and all the MQ-owned
	// value fields and maps have been updated.
	//
	// Now need to set all of the real items with the correct values
	if first {
		// Always ignore the first loop through as there might
		// be accumulated stuff from a while ago, and lead to
		// a misleading range on graphs.
		first = false
	} else {

		t := time.Now()
		j.CollectionTime.TimeStamp = t.Format(time.RFC3339)
		j.CollectionTime.Epoch = t.Unix()

		// All of the metrics for a given set of tags are printed in a single
		// JSON object.
		ptMap := make(map[string]pointsStruct)
		var pt pointsStruct
		var ok bool

		for _, cl := range mqmetric.Metrics.Classes {
			for _, ty := range cl.Types {
				for _, elem := range ty.Elements {
					for key, value := range elem.Values {

						if pt, ok = ptMap[key]; !ok {
							pt = pointsStruct{}
							pt.Tags = make(map[string]string)
							pt.Metric = make(map[string]float64)

							pt.Tags["qmgr"] = config.qMgrName
							pt.ObjectType = "queueManager"
							pt.Tags["platform"] = platformString
							if key != mqmetric.QMgrMapKey {
								pt.Tags["queue"] = key
								pt.ObjectType = "queue"
							}
						}

						pt.Metric[fixup(elem.MetricName)] = mqmetric.Normalise(elem, key, value)
						ptMap[key] = pt
					}
				}
			}
		}

		// After all the points have been created, add them to the JSON structure
		// for printing out
		for _, pt := range ptMap {
			j.Points = append(j.Points, pt)
		}

		// Next we extract the info for channel status. Several of the attributes
		// are used to build the tags that uniquely identify a channel instance.
		if pollStatus {
			ptMap := make(map[string]pointsStruct)

			for _, attr := range mqmetric.ChannelStatus.Attributes {
				for key, value := range attr.Values {
					if value.IsInt64 {

						chlType := int(mqmetric.ChannelStatus.Attributes[mqmetric.ATTR_CHL_TYPE].Values[key].ValueInt64)
						chlTypeString := strings.Replace(ibmmq.MQItoString("CHT", chlType), "MQCHT_", "", -1)
						// Not every channel status report has the RQMNAME attribute (eg SVRCONNs)
						rqmName := ""
						if rqmNameAttr, ok := mqmetric.ChannelStatus.Attributes[mqmetric.ATTR_CHL_RQMNAME].Values[key]; ok {
							rqmName = rqmNameAttr.ValueString
						}

						chlName := mqmetric.ChannelStatus.Attributes[mqmetric.ATTR_CHL_NAME].Values[key].ValueString
						connName := mqmetric.ChannelStatus.Attributes[mqmetric.ATTR_CHL_CONNNAME].Values[key].ValueString
						jobName := mqmetric.ChannelStatus.Attributes[mqmetric.ATTR_CHL_JOBNAME].Values[key].ValueString
						key := chlName + "/" + connName + "/" + jobName + "/" + rqmName

						if pt, ok = ptMap[key]; !ok {
							pt = pointsStruct{}
							pt.ObjectType = "channel"
							pt.Tags = make(map[string]string)
							pt.Metric = make(map[string]float64)

							pt.Tags["qmgr"] = strings.TrimSpace(config.qMgrName)
							pt.Tags["channel"] = chlName
							pt.Tags["platform"] = platformString
							pt.Tags[mqmetric.ATTR_CHL_TYPE] = strings.TrimSpace(chlTypeString)
							pt.Tags[mqmetric.ATTR_CHL_RQMNAME] = strings.TrimSpace(rqmName)
							pt.Tags[mqmetric.ATTR_CHL_CONNNAME] = strings.TrimSpace(connName)
							pt.Tags[mqmetric.ATTR_CHL_JOBNAME] = strings.TrimSpace(jobName)

						}
						pt.Metric[fixup(attr.MetricName)] = mqmetric.ChannelNormalise(attr, value.ValueInt64)
						ptMap[key] = pt
					}
				}

				for _, attr := range mqmetric.QueueStatus.Attributes {
					for key, value := range attr.Values {
						if value.IsInt64 {
							qName := mqmetric.QueueStatus.Attributes[mqmetric.ATTR_Q_NAME].Values[key].ValueString
							key := qName

							if pt, ok = ptMap[key]; !ok {
								pt = pointsStruct{}
								pt.ObjectType = "queue"
								pt.Metric = make(map[string]float64)
								pt.Tags = make(map[string]string)
								pt.Tags["qmgr"] = strings.TrimSpace(config.qMgrName)
								pt.Tags["queue"] = qName
								pt.Tags["platform"] = platformString
							}

							pt.Metric[fixup(attr.MetricName)] = mqmetric.QueueNormalise(attr, value.ValueInt64)
							ptMap[key] = pt
						}
					}
				}
			}

			for _, pt := range ptMap {
				j.Points = append(j.Points, pt)
			}
		}

		b, _ := json.MarshalIndent(j, "", "  ")
		fmt.Printf("%s\n", b)

		postToUrl("http://10.40.10.72:5059",b)

	}

	return err

} 
func postToUrl(url String, jsonStr String) {
   // url := "http://restapi3.apiary.io/notes"
    fmt.Println("URL:>", url)

   // var jsonStr = []byte(`{"title":"Buy cheese and bread for breakfast."}`)
    req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
    req.Header.Set("Content-Type", "application/json")

    client := &http.Client{}
    resp, err := client.Do(req)
    if err != nil {
        panic(err)
    }
    defer resp.Body.Close()

    fmt.Println("response Status:", resp.Status)
    fmt.Println("response Headers:", resp.Header)
    body, _ := ioutil.ReadAll(resp.Body)
    fmt.Println("response Body:", string(body))
}

func fixup(s1 string) string {
	// Another reformatting of the metric name - this one converts
	// something like queue_avoided_bytes into queueAvoidedBytes
	s2 := ""
	c := ""
	nextCaseUpper := false

	for i := 0; i < len(s1); i++ {
		if s1[i] != '_' {
			if nextCaseUpper {
				c = strings.ToUpper(s1[i : i+1])
				nextCaseUpper = false
			} else {
				c = strings.ToLower(s1[i : i+1])
			}
			s2 += c
		} else {
			nextCaseUpper = true
		}

	}
	return s2
}
