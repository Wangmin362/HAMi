/*
Copyright 2024 The HAMi Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"net/http"

	"github.com/Project-HAMi/HAMi/pkg/device"
	"github.com/Project-HAMi/HAMi/pkg/scheduler"
	"github.com/Project-HAMi/HAMi/pkg/scheduler/config"
	"github.com/Project-HAMi/HAMi/pkg/scheduler/routes"
	"github.com/Project-HAMi/HAMi/pkg/util"
	"github.com/Project-HAMi/HAMi/pkg/version"

	"github.com/julienschmidt/httprouter"
	"github.com/spf13/cobra"
	klog "k8s.io/klog/v2"
)

//var version string

var (
	sher        *scheduler.Scheduler
	tlsKeyFile  string
	tlsCertFile string
	rootCmd     = &cobra.Command{
		Use:   "scheduler",
		Short: "kubernetes vgpu scheduler",
		Run: func(cmd *cobra.Command, args []string) {
			start()
		},
	}
)

func init() {
	rootCmd.Flags().SortFlags = false
	rootCmd.PersistentFlags().SortFlags = false

	rootCmd.Flags().StringVar(&config.HTTPBind, "http_bind", "127.0.0.1:8080", "http server bind address")
	rootCmd.Flags().StringVar(&tlsCertFile, "cert_file", "", "tls cert file")
	rootCmd.Flags().StringVar(&tlsKeyFile, "key_file", "", "tls key file")
	rootCmd.Flags().StringVar(&config.SchedulerName, "scheduler-name", "", "the name to be added to pod.spec.schedulerName if not empty")
	rootCmd.Flags().Int32Var(&config.DefaultMem, "default-mem", 0, "default gpu device memory to allocate")
	rootCmd.Flags().Int32Var(&config.DefaultCores, "default-cores", 0, "default gpu core percentage to allocate")
	rootCmd.Flags().Int32Var(&config.DefaultResourceNum, "default-gpu", 1, "default gpu to allocate")
	rootCmd.Flags().StringVar(&config.NodeSchedulerPolicy, "node-scheduler-policy", util.NodeSchedulerPolicyBinpack.String(), "node scheduler policy")
	rootCmd.Flags().StringVar(&config.GPUSchedulerPolicy, "gpu-scheduler-policy", util.GPUSchedulerPolicySpread.String(), "GPU scheduler policy")
	rootCmd.Flags().StringVar(&config.MetricsBindAddress, "metrics-bind-address", ":9395", "The TCP address that the scheduler should bind to for serving prometheus metrics(e.g. 127.0.0.1:9395, :9395)")
	rootCmd.Flags().StringToStringVar(&config.NodeLabelSelector, "node-label-selector", nil, "key=value pairs separated by commas")
	rootCmd.PersistentFlags().AddGoFlagSet(device.GlobalFlagSet())
	rootCmd.AddCommand(version.VersionCmd)
	rootCmd.Flags().AddGoFlagSet(util.InitKlogFlags())
}

func start() {
	device.InitDevices()
	sher = scheduler.NewScheduler()
	// 启动Infromer，并且等待Pod，node资源同步完成
	sher.Start()
	defer sher.Stop()

	// start monitor metrics
	// 获取当前集群中各个节点的资源信息
	go sher.RegisterFromNodeAnnotations()
	// 向外暴露指标接口
	go initMetrics(config.MetricsBindAddress)

	// start http server
	router := httprouter.New()
	// extender回调接口
	router.POST("/filter", routes.PredicateRoute(sher))
	router.POST("/bind", routes.Bind(sher))
	// 1. webhook回调接口
	// 2. webhook的核心功能其实就是把Pod的schedulerName设置为hami-scheduler. 这样这些Pod后续的调度就会交给hami-scheduler来处理
	// 3. 但是webhook也并非修改所有的Pod的调度器都设置为hami-scheduler, 而是根据Pod的申请资源来决定的，如果Pod的申请资源当中包含了
	// gpu, 昇腾，天数，摩尔线程等等厂商的资源，那么就会把Pod的调度器设置为hami-scheduler, 否则就忽略这个Pod，相当于交给默认的调度器来处理
	// 4. 另外，如果当前Pod的nodeName已经被设置了，说明用户在创建这个Pod的时候就希望这个Pod被调度到指定的节点上，此时webhook也会忽略这个Pod，
	// 显然，对于这种类型的Pod，很有可能节点不足，存在Pod一直处于Pending状态的情况
	router.POST("/webhook", routes.WebHookRoute())
	router.GET("/healthz", routes.HealthzRoute())
	klog.Info("listen on ", config.HTTPBind)
	if len(tlsCertFile) == 0 || len(tlsKeyFile) == 0 {
		if err := http.ListenAndServe(config.HTTPBind, router); err != nil {
			klog.Fatal("Listen and Serve error, ", err)
		}
	} else {
		if err := http.ListenAndServeTLS(config.HTTPBind, tlsCertFile, tlsKeyFile, router); err != nil {
			klog.Fatal("Listen and Serve error, ", err)
		}
	}
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		klog.Fatal(err)
	}
}
