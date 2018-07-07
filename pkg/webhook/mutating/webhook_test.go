package mutating_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	admissionv1beta1 "k8s.io/api/admission/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/slok/kutator/pkg/log"
	"github.com/slok/kutator/pkg/webhook"
	"github.com/slok/kutator/pkg/webhook/mutating"
)

var patchTypeJSONPatch = func() *admissionv1beta1.PatchType {
	pt := admissionv1beta1.PatchTypeJSONPatch
	return &pt
}()

func getPodJSON() []byte {
	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testPod",
			Namespace: "testNS",
			Annotations: map[string]string{
				"key1": "val1",
				"key2": "val2",
				"key3": "val3",
				"key4": "val4",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "container1",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("10m"),
							corev1.ResourceMemory: resource.MustParse("10Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("100Mi"),
						},
					},
				},
				{
					Name: "container2",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("30m"),
							corev1.ResourceMemory: resource.MustParse("30Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("70m"),
							corev1.ResourceMemory: resource.MustParse("70Mi"),
						},
					},
				},
			},
		},
	}
	bs, _ := json.Marshal(pod)
	return bs
}

func getPodNSMutator(ns string) mutating.Mutator {
	return mutating.MutatorFunc(func(_ context.Context, obj metav1.Object) (bool, error) {
		pod, ok := obj.(*corev1.Pod)
		if !ok {
			return true, fmt.Errorf("not a pod")
		}

		pod.Namespace = ns

		return false, nil
	})
}

func getPodAnnotationsReplacerMutator(annotations map[string]string) mutating.Mutator {
	return mutating.MutatorFunc(func(_ context.Context, obj metav1.Object) (bool, error) {
		pod, ok := obj.(*corev1.Pod)
		if !ok {
			return true, fmt.Errorf("not a pod")
		}

		pod.Annotations = annotations

		return false, nil
	})
}

func getPodResourceLimitDeletorMutator() mutating.Mutator {
	return mutating.MutatorFunc(func(_ context.Context, obj metav1.Object) (bool, error) {
		pod, ok := obj.(*corev1.Pod)
		if !ok {
			return true, fmt.Errorf("not a pod")
		}

		for idx := range pod.Spec.Containers {
			c := pod.Spec.Containers[idx]
			c.Resources.Limits = nil
			pod.Spec.Containers[idx] = c
		}

		return false, nil
	})
}

func TestDynamicMutationWebhook(t *testing.T) {
	f := func(m mutating.Mutator) webhook.Webhook {
		return mutating.NewDynamicWebhook(m, log.Dummy)
	}

	testPodAdmissionReviewMutation(f, t)
}

func TestStaticMutationWebhook(t *testing.T) {
	f := func(m mutating.Mutator) webhook.Webhook {
		wh, err := mutating.NewStaticWebhook(m, &corev1.Pod{}, log.Dummy)
		assert.NoError(t, err)
		return wh
	}

	testPodAdmissionReviewMutation(f, t)
}

type whfactory func(mutating.Mutator) webhook.Webhook

func testPodAdmissionReviewMutation(whf whfactory, t *testing.T) {
	tests := []struct {
		name        string
		mutator     mutating.Mutator
		review      *admissionv1beta1.AdmissionReview
		expResponse *admissionv1beta1.AdmissionResponse
	}{
		{
			name:    "a review of a Pod with an ns mutator should mutate the ns",
			mutator: getPodNSMutator("myChangedNS"),
			review: &admissionv1beta1.AdmissionReview{
				Request: &admissionv1beta1.AdmissionRequest{
					UID: "test",
					Object: runtime.RawExtension{
						Raw: getPodJSON(),
					},
				},
			},
			expResponse: &admissionv1beta1.AdmissionResponse{
				UID:       "test",
				Patch:     []byte(`{"metadata":{"namespace":"myChangedNS"}}`),
				Allowed:   true,
				PatchType: patchTypeJSONPatch,
			},
		},
		{
			name: "a review of a Pod with an annotations mutator should mutate the annotations",
			mutator: getPodAnnotationsReplacerMutator(map[string]string{
				"key1": "val1_mutated",
				"key2": "val2",
				"key4": "val4",
				"key5": "val5",
			}),
			review: &admissionv1beta1.AdmissionReview{
				Request: &admissionv1beta1.AdmissionRequest{
					UID: "test",
					Object: runtime.RawExtension{
						Raw: getPodJSON(),
					},
				},
			},
			expResponse: &admissionv1beta1.AdmissionResponse{
				UID:       "test",
				Patch:     []byte(`{"metadata":{"annotations":{"key1":"val1_mutated","key3":null,"key5":"val5"}}}`),
				Allowed:   true,
				PatchType: patchTypeJSONPatch,
			},
		},
		{
			name:    "a review of a Pod with an limit deletion mutator should delete the limi resources from a pod",
			mutator: getPodResourceLimitDeletorMutator(),
			review: &admissionv1beta1.AdmissionReview{
				Request: &admissionv1beta1.AdmissionRequest{
					UID: "test",
					Object: runtime.RawExtension{
						Raw: getPodJSON(),
					},
				},
			},
			expResponse: &admissionv1beta1.AdmissionResponse{
				UID:       "test",
				Patch:     []byte(`{"spec":{"containers":[{"name":"container1","resources":{"requests":{"cpu":"10m","memory":"10Mi"}}},{"name":"container2","resources":{"requests":{"cpu":"30m","memory":"30Mi"}}}]}}`),
				Allowed:   true,
				PatchType: patchTypeJSONPatch,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert := assert.New(t)
			wh := whf(test.mutator)
			gotResponse := wh.Review(test.review)
			assert.Equal(test.expResponse, gotResponse)
		})
	}
}

/*
func BenchmarkDynamicPodAdmissionReviewMutation(b *testing.B) {
	wh := webhook.NewDynamicWebhook()
	benchmarkPodAdmissionReviewMutation(wh, b)
}

func BenchmarkStaticPodAdmissionReviewMutation(b *testing.B) {
	wh, _ := webhook.NewStaticWebhook(&corev1.Pod{})
	benchmarkPodAdmissionReviewMutation(wh, b)
}

func benchmarkPodAdmissionReviewMutation(wh webhook.Webhook, b *testing.B) {
	for i := 0; i < b.N; i++ {
		mutator := getPodNSMutator("myChangedNS")
		ar := &admissionv1beta1.AdmissionReview{
			Request: &admissionv1beta1.AdmissionRequest{
				UID: "test",
				Object: runtime.RawExtension{
					Raw: getPodJSON(),
				},
			},
		}
		wh.MutationAdmissionReview(mutator, ar)
	}
}
*/