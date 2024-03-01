package main

import (
	"context"
	"fmt"
	"io/ioutil"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"os"
	"strconv"
	"time"
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
	dynClient, _ := dynamic.NewForConfig(config)
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
						increaseDeploymentReplica(clientset, dynClient, involvedObject.Name)
					}
				}
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				event := newObj.(*corev1.Event)
				if event.Reason == "DisruptionBlocked" {
					involvedObject := event.InvolvedObject
					fmt.Printf("Involved Object: %s/%s\n", involvedObject.Kind, involvedObject.Name)
					if involvedObject.Kind == "Node" {
						increaseDeploymentReplica(clientset, dynClient, involvedObject.Name)
					}
				}
			},
		},
	)

	stop := make(chan struct{})
	go controller.Run(stop)

	// Add a ticker to trigger the label update logic every 5 minutes
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			updateLabels(clientset)
		}
	}
}

func updateLabels(clientset *kubernetes.Clientset) {
	deployments, err := clientset.AppsV1().Deployments(metav1.NamespaceAll).List(
		context.TODO(), metav1.ListOptions{
			LabelSelector: "tags.xquare.app/default-replicas",
		},
	)
	if err != nil {
		fmt.Printf("Error listing deployments: %v\n", err)
		return
	}

	for _, deployment := range deployments.Items {
		currentReplicaCountStr, ok := deployment.ObjectMeta.Labels["tags.xquare.app/default-replicas"]
		if !ok {
			fmt.Printf("Label tags.xquare.app/default-replicas not found for deployment %s\n", deployment.Name)
			continue
		}
		currentReplicaCount, err := strconv.Atoi(currentReplicaCountStr)
		if err != nil {
			fmt.Printf("Error converting currentReplicaCountStr to int for deployment %s: %v\n", deployment.Name, err)
			continue
		}
		int32ReplicaCount := int32(currentReplicaCount)
		deployment.Spec.Replicas = &int32ReplicaCount
		delete(deployment.ObjectMeta.Labels, "tags.xquare.app/default-replicas")
		_, err = clientset.AppsV1().Deployments(deployment.Namespace).Update(
			context.TODO(), &deployment, metav1.UpdateOptions{},
		)
		if err != nil {
			fmt.Printf("Error updating replicas for deployment %s: %v\n", deployment.Name, err)
			continue
		}
		fmt.Printf("Updated replicas for deployment %s to %d\n", deployment.Name, currentReplicaCount)
	}
}

func increaseDeploymentReplica(clientset *kubernetes.Clientset, dynClient dynamic.Interface, nodeName string) {
	pods, err := clientset.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + nodeName,
	})
	if err != nil {
		panic(err)
	}

	for _, pod := range pods.Items {
		appValue := pod.Labels["app"]
		typeValue := pod.Labels["type"]
		fmt.Printf("app:%s  type:%s\n", appValue, typeValue)
		if appValue != "" && ( typeValue == "test" || typeValue == "fe" || typeValue == "be" ){
			deployments, err := clientset.AppsV1().Deployments(pod.Namespace).List(
				context.TODO(), metav1.ListOptions{LabelSelector: fmt.Sprintf("app=%s", appValue)},
			)
			if err == nil && len(deployments.Items) > 0 {
				deployment := deployments.Items[0]
				currentReplicaCount := *deployment.Spec.Replicas
				newReplicaCount := int32(2)
				deployment.Spec.Replicas = &newReplicaCount
				deployment.ObjectMeta.Labels["tags.xquare.app/default-replicas"] = fmt.Sprint("1")
				_, err = clientset.AppsV1().Deployments(pod.Namespace).Update(
					context.TODO(), &deployment, metav1.UpdateOptions{},
				)
				fmt.Printf("Increase deployment %s's replica %d to %d\n", appValue, currentReplicaCount, newReplicaCount)
				if err != nil {
					fmt.Printf(err.Error())
				}
			}
		}
	}
}
