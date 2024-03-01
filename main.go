package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"k8s.io/apimachinery/pkg/fields"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

func main() {
	token, err := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
	if err != nil {
		fmt.Printf("Unable to read service account token: %v", err)
		os.Exit(1)
	}

	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}

	config.BearerToken = string(token)

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	watchlist := cache.NewListWatchFromClient(clientset.CoreV1().RESTClient(), "events", metav1.NamespaceAll, fields.Everything())
	_, controller := cache.NewInformer(
		watchlist,
		&corev1.Event{},
		0,
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				event := obj.(*corev1.Event)
				if event.Reason == "DisruptionBlocked" {
					involvedObject := event.InvolvedObject
					fmt.Printf("Involved Object: %s/%s\n", involvedObject.Kind, involvedObject.Name)
					if involvedObject.Kind == "Node" {
						increaseDeploymentReplica(clientset, involvedObject.Name)
					}
				}
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				event := newObj.(*corev1.Event)
				if event.Reason == "DisruptionBlocked" {
					involvedObject := event.InvolvedObject
					fmt.Printf("Involved Object: %s/%s\n", involvedObject.Kind, involvedObject.Name)
					if involvedObject.Kind == "Node" {
						increaseDeploymentReplica(clientset, involvedObject.Name)
					}
				}
			},
		},
	)

	stop := make(chan struct{})
	go controller.Run(stop)
	for {
		time.Sleep(time.Second)
	}
}

func increaseDeploymentReplica(clientset *kubernetes.Clientset, nodeName string) {
	pods, err := clientset.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + nodeName,
	})
	if err != nil {
		panic(err)
	}

	for _, pod := range pods.Items {
		appValue := pod.Labels["app"]
		typeValue := pod.Labels["type"]
		fmt.Printf("app:%s  type:%s\n",appValue, typeValue)
		if appValue != "" && (typeValue == "test") {

			deployments, err := clientset.AppsV1().Deployments(pod.Namespace).List(
				context.TODO(), metav1.ListOptions{LabelSelector: fmt.Sprintf("app=%s", appValue)},
			)

			if err == nil && len(deployments.Items) > 0 {
				deployment := deployments.Items[0]
				newReplicaCount := int32(2)
				deployment.Spec.Replicas = &newReplicaCount

				_, err := clientset.AppsV1().Deployments(pod.Namespace).Update(
					context.TODO(), &deployment, metav1.UpdateOptions{},
				)
				fmt.Printf("Increase deployment %s's replica %d to %d\n", appValue, 1, newReplicaCount)
				if err != nil {
					fmt.Printf(err.Error())
				}
			}
		}
	}
}
