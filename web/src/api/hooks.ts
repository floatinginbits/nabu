import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { client } from "./client";

export function useTasks() {
  return useQuery({
    queryKey: ["tasks"],
    queryFn: async () => {
      const { data, error } = await client.GET("/api/v1/tasks");
      if (error) throw new Error(error.error.message);
      return data;
    },
  });
}

export function useCreateTask() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (title: string) => {
      const { data, error } = await client.POST("/api/v1/tasks", {
        body: { title },
      });
      if (error) throw new Error(error.error.message);
      return data;
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["tasks"] }),
  });
}
