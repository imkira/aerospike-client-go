// Copyright 2013-2015 Aerospike, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package aerospike_test

import (
	"fmt"
	"time"

	. "github.com/aerospike/aerospike-client-go"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

// ALL tests are isolated by SetName and Key, which are 50 random charachters
var _ = Describe("Aerospike", func() {
	initTestVars()

	Describe("Node Connection Pool", func() {
		// connection data
		var err error
		var client *Client

		BeforeEach(func() {
			// use the same client for all
			client, err = NewClientWithPolicy(clientPolicy, *host, *port)
			Expect(err).ToNot(HaveOccurred())
		})

		Context("When No Connection Count Limit Is Set", func() {

			It("must return a new connection on every poll", func() {
				clientPolicy := NewClientPolicy()
				clientPolicy.LimitConnectionsToQueueSize = false
				clientPolicy.ConnectionQueueSize = 4

				client, err = NewClientWithPolicy(clientPolicy, *host, *port)
				Expect(err).ToNot(HaveOccurred())
				defer client.Close()

				node := client.GetNodes()[0]

				for i := 0; i < 20; i++ {
					c, err := node.GetConnection(0)
					Expect(err).NotTo(HaveOccurred())
					Expect(c).NotTo(BeNil())
					Expect(c.IsConnected()).To(BeTrue())

					c.Close()
				}

			})

		})

		Context("When A Connection Count Limit Is Set", func() {

			It("must return an error when maximum number of connections are polled", func() {
				clientPolicy := NewClientPolicy()
				clientPolicy.LimitConnectionsToQueueSize = true
				clientPolicy.ConnectionQueueSize = 4

				client, err = NewClientWithPolicy(clientPolicy, *host, *port)
				Expect(err).ToNot(HaveOccurred())
				defer client.Close()

				node := client.GetNodes()[0]

				for i := 0; i < 4; i++ {
					c, err := node.GetConnection(0)
					Expect(err).NotTo(HaveOccurred())
					Expect(c).NotTo(BeNil())
					Expect(c.IsConnected()).To(BeTrue())

					c.Close()
				}

				for i := 0; i < 4; i++ {
					t := time.Now()
					_, err := node.GetConnection(0)
					Expect(err).To(HaveOccurred())
					Expect(time.Now().Sub(t)).To(BeNumerically(">=", time.Millisecond))
					Expect(time.Now().Sub(t)).To(BeNumerically("<", 2*time.Millisecond))
				}

			})

		})

		Context("When A Maximum Idle Duration Is Set", func() {

			It("must reuse connections before they become idle", func() {
				clientPolicy := NewClientPolicy()
				clientPolicy.MaxIdle = 1000 * time.Millisecond
				clientPolicy.TendInterval = time.Hour

				client, err = NewClientWithPolicy(clientPolicy, *host, *port)
				Expect(err).ToNot(HaveOccurred())
				defer client.Close()

				node := client.GetNodes()[0]

				// get a few connections at once
				var conns []*Connection
				for i := 0; i < 4; i++ {
					By(fmt.Sprintf("Retrieving conns i=%d", i))
					c, err := node.GetConnection(0)
					Expect(err).NotTo(HaveOccurred())
					Expect(c).NotTo(BeNil())
					Expect(c.IsConnected()).To(BeTrue())

					conns = append(conns, c)
				}

				// return them to the pool
				for _, c := range conns {
					node.PutConnection(c)
				}

				start := time.Now()
				estimatedDeadline := start.Add(clientPolicy.MaxIdle)
				deadlineThreshold := clientPolicy.MaxIdle / 10

				// make sure the same connections are all retrieved again
				checkCount := 0
				for estimatedDeadline.Sub(time.Now()) > deadlineThreshold {
					checkCount++
					By(fmt.Sprintf("Retrieving conns2 checkCount=%d", checkCount))
					var conns2 []*Connection
					for i := 0; i < len(conns); i++ {
						c, err := node.GetConnection(0)
						Expect(err).NotTo(HaveOccurred())
						Expect(c).NotTo(BeNil())
						Expect(c.IsConnected()).To(BeTrue())
						Expect(conns).To(ContainElement(c))
						Expect(conns2).NotTo(ContainElement(c))

						conns2 = append(conns2, c)
					}

					// just put them in the pool
					for _, c := range conns2 {
						node.PutConnection(c)
					}

					time.Sleep(time.Millisecond)
				}

				// we should be called lots of times
				Expect(checkCount).To(BeNumerically(">", 500))

				// sleep again until all connections are all idle
				time.Sleep(clientPolicy.MaxIdle)

				// get connections again, making sure they are all new
				var conns3 []*Connection
				for i := 0; i < len(conns); i++ {
					By(fmt.Sprintf("Retrieving conns3 i=%d", i))
					c, err := node.GetConnection(0)
					Expect(err).NotTo(HaveOccurred())
					Expect(c).NotTo(BeNil())
					Expect(c.IsConnected()).To(BeTrue())

					Expect(conns).NotTo(ContainElement(c))
					Expect(conns3).NotTo(ContainElement(c))

					conns3 = append(conns3, c)
				}

				// refresh and return them to the pool
				for _, c := range conns {
					Expect(c.IsConnected()).To(BeFalse())
				}

				// don't forget to close connections
				for _, c := range conns3 {
					c.Close()
				}
			})

			It("must delay the connection from becoming idle if it is put back in the queue", func() {
				clientPolicy := NewClientPolicy()
				clientPolicy.MaxIdle = 1000 * time.Millisecond
				clientPolicy.TendInterval = time.Hour

				client, err = NewClientWithPolicy(clientPolicy, *host, *port)
				Expect(err).ToNot(HaveOccurred())
				defer client.Close()

				node := client.GetNodes()[0]

				deadlineThreshold := clientPolicy.MaxIdle / 10

				By("Retrieving c")
				c, err := node.GetConnection(0)
				Expect(err).NotTo(HaveOccurred())
				Expect(c).NotTo(BeNil())
				Expect(c.IsConnected()).To(BeTrue())
				node.PutConnection(c)

				// continuously refresh the connection just before it goes idle
				var lastRefresh time.Time
				for i := 0; i < 3; i++ {
					time.Sleep(clientPolicy.MaxIdle - deadlineThreshold)
					By(fmt.Sprintf("Retrieving c2 i=%d", i))

					c2, err := node.GetConnection(0)
					Expect(err).NotTo(HaveOccurred())
					Expect(c2).NotTo(BeNil())
					Expect(c2).To(Equal(c))
					Expect(c2.IsConnected()).To(BeTrue())

					lastRefresh = time.Now()
					node.PutConnection(c2)
				}

				// wait about the required time to become idle
				for time.Now().Sub(lastRefresh) <= clientPolicy.MaxIdle {
					By("Sleeping")
					time.Sleep(1 * time.Millisecond)
				}

				// we should get a new connection
				c3, err := node.GetConnection(0)
				Expect(err).NotTo(HaveOccurred())
				Expect(c3).NotTo(BeNil())
				defer c3.Close()
				Expect(c3).ToNot(Equal(c))
				Expect(c3.IsConnected()).To(BeTrue())

				// the original connection should be closed
				Expect(c.IsConnected()).To(BeFalse())
			})

		})
	})
})
